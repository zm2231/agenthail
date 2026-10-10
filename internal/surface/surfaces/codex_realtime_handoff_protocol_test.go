//go:build voice_protocol

package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/voice"
)

func TestInstalledCodexRealtimeDelegationOwnership(t *testing.T) {
	binary := os.Getenv("AGENTHAIL_CODEX_PROTOCOL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Skip("requires an explicitly selected Codex binary")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	frames := make(chan json.RawMessage, 128)
	connections := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if websocket.IsWebSocketUpgrade(r) {
			conn, err := upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			defer conn.Close()
			connections <- conn
			for {
				_, data, err := conn.ReadMessage()
				if err != nil {
					return
				}
				select {
				case frames <- data:
				case <-ctx.Done():
					return
				}
			}
		}
		if strings.HasSuffix(r.URL.Path, "/responses") {
			w.Header().Set("Content-Type", "text/event-stream")
			message := map[string]any{"id": "synthetic-answer", "type": "message", "role": "assistant", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Synthetic handoff result", "annotations": []any{}}}}
			progress := map[string]any{"id": "synthetic-progress", "type": "message", "role": "assistant", "phase": "commentary", "status": "completed", "content": []any{map[string]any{"type": "output_text", "text": "Checking synthetic work", "annotations": []any{}}}}
			message["phase"] = "final_answer"
			for _, event := range []map[string]any{
				{"type": "response.created", "response": map[string]any{"id": "synthetic-response", "status": "in_progress"}},
				{"type": "response.output_item.added", "output_index": 0, "item": map[string]any{"id": "synthetic-progress", "type": "message", "role": "assistant", "phase": "commentary", "content": []any{}}},
				{"type": "response.output_item.done", "output_index": 0, "item": progress},
				{"type": "response.output_item.added", "output_index": 1, "item": map[string]any{"id": "synthetic-answer", "type": "message", "role": "assistant", "phase": "final_answer", "content": []any{}}},
				{"type": "response.output_text.delta", "item_id": "synthetic-answer", "output_index": 1, "content_index": 0, "delta": "Synthetic handoff result"},
				{"type": "response.output_item.done", "output_index": 1, "item": message},
				{"type": "response.completed", "response": map[string]any{"id": "synthetic-response", "status": "completed", "output": []any{progress, message}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
			} {
				data, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
			}
			return
		}
		w.Header().Set("Content-Type", "application/sdp")
		w.Header().Set("Location", "/v1/realtime/calls/rtc_synthetic_handoff")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, "v=0\r\ns=synthetic\r\n")
	}))
	defer server.Close()
	provider := fmt.Sprintf(`{name="Synthetic",base_url=%q,wire_api="responses",requires_openai_auth=false}`, server.URL+"/v1")
	cmd := exec.CommandContext(ctx, binary, "app-server", "--stdio", "-c", `model_provider="synthetic"`, "-c", "model_providers.synthetic="+provider, "-c", fmt.Sprintf("experimental_realtime_ws_base_url=%q", server.URL), "-c", fmt.Sprintf("experimental_realtime_webrtc_call_base_url=%q", server.URL))
	cmd.Env = append(os.Environ(), "CODEX_HOME="+t.TempDir(), "OPENAI_API_KEY=synthetic-handoff-test")
	cmd.Stderr = io.Discard
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { stdin.Close(); cancel(); _ = cmd.Wait() }()
	responses := make(chan map[string]any, 256)
	go func() {
		defer close(responses)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		for scanner.Scan() {
			var value map[string]any
			if json.Unmarshal(scanner.Bytes(), &value) == nil {
				select {
				case responses <- value:
				case <-ctx.Done():
					return
				}
			}
		}
	}()
	next := 0
	request := func(method string, params map[string]any) map[string]any {
		t.Helper()
		next++
		if err := json.NewEncoder(stdin).Encode(map[string]any{"id": next, "method": method, "params": params}); err != nil {
			t.Fatal(err)
		}
		for {
			select {
			case value, ok := <-responses:
				if !ok {
					t.Fatal("disposable app-server exited")
				}
				if value["id"] == float64(next) {
					if value["error"] != nil {
						t.Fatalf("%s: %v", method, value["error"])
					}
					return value
				}
			case <-ctx.Done():
				t.Fatalf("%s timed out", method)
			}
		}
	}
	request("initialize", map[string]any{"clientInfo": map[string]any{"name": "agenthail-handoff-probe", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}})
	_ = json.NewEncoder(stdin).Encode(map[string]any{"method": "initialized"})
	created := request("thread/start", map[string]any{"cwd": t.TempDir(), "ephemeral": true, "model": "synthetic-model", "approvalPolicy": "never", "sandbox": "read-only"})
	result, _ := created["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	id, _ := thread["id"].(string)
	params := voice.StartParams(id, "synthetic-handoff", "v=0 synthetic", false)
	request("thread/realtime/start", params)
	var conn *websocket.Conn
	select {
	case conn = <-connections:
	case <-ctx.Done():
		t.Fatal("realtime sideband did not connect")
	}
	for _, delegationID := range []string{"first", "follow-up", "repeated-question"} {
		if err := conn.WriteJSON(map[string]any{"type": "delegation.created", "item": map[string]any{"id": delegationID, "type": "delegation", "target": "client", "content": []any{map[string]any{"type": "input_text", "text": "Say Synthetic handoff result"}}}}); err != nil {
			t.Fatal(err)
		}
		matched, progressReceived := false, false
		for !matched {
			select {
			case frame := <-frames:
				t.Logf("automatic handoff frame: %s", frame)
				var value map[string]any
				_ = json.Unmarshal(frame, &value)
				progressReceived = progressReceived || (value["type"] == "delegation.context.append" && value["delegation_item_id"] == delegationID && strings.Contains(string(frame), "Checking synthetic work"))
				matched = value["type"] == "delegation.context.append" && value["delegation_item_id"] == delegationID && strings.Contains(string(frame), "Synthetic handoff result")
			case <-ctx.Done():
				t.Fatalf("%s did not return a correlated delegation result", delegationID)
			}
		}
		if !progressReceived {
			t.Fatalf("%s lost its request-owned progress", delegationID)
		}
		completed := false
		for !completed {
			select {
			case notification, ok := <-responses:
				if !ok {
					t.Fatal("disposable app-server exited")
				}
				completed = notification["method"] == "turn/completed"
			case <-ctx.Done():
				t.Fatalf("%s backend turn did not complete", delegationID)
			}
		}
	}
	request("thread/realtime/stop", map[string]any{"threadId": id})
}

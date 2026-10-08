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
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestInstalledCodexRealtimeContextChannels(t *testing.T) {
	binary := os.Getenv("AGENTHAIL_CODEX_PROTOCOL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Skip("requires an explicitly selected Codex binary; synthetic loopback transport only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	frames := make(chan json.RawMessage, 16)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(20 * time.Second))
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
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, binary, "app-server", "--stdio", "-c", fmt.Sprintf("experimental_realtime_ws_base_url=%q", server.URL))
	cmd.Env = append(os.Environ(), "CODEX_HOME="+t.TempDir(), "OPENAI_API_KEY=agenthail-synthetic-protocol-test")
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
	responses := make(chan map[string]any, 32)
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
			case response, ok := <-responses:
				if !ok {
					t.Fatal("isolated app-server closed")
				}
				if response["id"] != float64(next) {
					continue
				}
				if response["error"] != nil {
					t.Fatalf("%s rejected: %v", method, response["error"])
				}
				return response
			case <-ctx.Done():
				t.Fatalf("%s timed out", method)
			}
		}
	}
	request("initialize", map[string]any{"clientInfo": map[string]any{"name": "agenthail-protocol-probe", "version": "1"}, "capabilities": map[string]any{"experimentalApi": true}})
	if err := json.NewEncoder(stdin).Encode(map[string]any{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	created := request("thread/start", map[string]any{"cwd": t.TempDir(), "ephemeral": true, "approvalPolicy": "never", "sandbox": "read-only"})
	result, _ := created["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	id, _ := thread["id"].(string)
	if id == "" {
		t.Fatal("no disposable thread ID")
	}
	request("thread/realtime/start", map[string]any{"threadId": id, "outputModality": "audio", "version": "v3", "includeStartupContext": false, "transport": map[string]any{"type": "existingCall", "callId": "rtc_synthetic_context_probe"}})
	for _, method := range []string{"thread/realtime/appendText", "thread/realtime/appendSpeech"} {
		params := map[string]any{"threadId": id, "text": "Synthetic selected-worker update"}
		if method == "thread/realtime/appendText" {
			params["role"] = "developer"
		}
		request(method, params)
		select {
		case frame := <-frames:
			t.Logf("%s frame=%s", method, frame)
			var value map[string]any
			if err := json.Unmarshal(frame, &value); err != nil {
				t.Fatal(err)
			}
			if value["type"] != "session.context.append" {
				t.Fatalf("unexpected context frame: %s", frame)
			}
			if method == "thread/realtime/appendSpeech" && value["channel"] != "speakable" {
				t.Fatalf("speech lacks speakable channel: %s", frame)
			}
			if method == "thread/realtime/appendText" && value["channel"] == "speakable" {
				t.Fatalf("quiet context became speakable: %s", frame)
			}
		case <-ctx.Done():
			t.Fatalf("%s produced no context frame", method)
		}
	}
	request("thread/realtime/stop", map[string]any{"threadId": id})
}

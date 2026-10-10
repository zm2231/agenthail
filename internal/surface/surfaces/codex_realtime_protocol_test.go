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
	"github.com/zm2231/agenthail/internal/voice"
)

func TestInstalledCodexRealtimeContextChannels(t *testing.T) {
	binary := os.Getenv("AGENTHAIL_CODEX_PROTOCOL_BINARY")
	if !filepath.IsAbs(binary) {
		t.Skip("requires an explicitly selected Codex binary; synthetic loopback transport only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	frames := make(chan json.RawMessage, 16)
	notifications := make(chan map[string]any, 128)
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !websocket.IsWebSocketUpgrade(r) {
			w.Header().Set("Content-Type", "application/sdp")
			w.Header().Set("Location", "/v1/realtime/calls/rtc_synthetic_context_probe")
			w.WriteHeader(http.StatusCreated)
			_, _ = io.WriteString(w, "v=0\r\ns=synthetic\r\n")
			return
		}
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
			var frame map[string]any
			_ = json.Unmarshal(data, &frame)
			if frame["channel"] == "speakable" {
				_ = conn.WriteJSON(map[string]any{"type": "output_transcript.added", "item": map[string]any{"id": "voice-output", "text": "Synthetic spoken response"}})
				_ = conn.WriteJSON(map[string]any{"type": "turn.done", "turn": map[string]any{"id": "voice-turn", "role": "assistant", "transcript": "Synthetic spoken response"}})
			}
		}
	}))
	defer server.Close()
	cmd := exec.CommandContext(ctx, binary, "app-server", "--stdio", "-c", fmt.Sprintf("experimental_realtime_ws_base_url=%q", server.URL), "-c", fmt.Sprintf("experimental_realtime_webrtc_call_base_url=%q", server.URL))
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
				if value["method"] != nil {
					select {
					case notifications <- value:
					default:
					}
				}
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
	request("thread/realtime/start", voice.StartParams(id, "synthetic-call", "v=0 synthetic offer", false))
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
			if method == "thread/realtime/appendText" {
				t.Log("appendText omits commentary; it must not transport quiet worker updates")
			}
		case <-ctx.Done():
			t.Fatalf("%s produced no context frame", method)
		}
	}
	started, completed := false, false
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	for !started || !completed {
		select {
		case notification := <-notifications:
			params, _ := notification["params"].(map[string]any)
			if params["role"] != "assistant" {
				continue
			}
			t.Logf("installed transcript lifecycle: %v", notification)
			if notification["method"] == "thread/realtime/transcript/delta" {
				started = true
			}
			if notification["method"] == "thread/realtime/transcript/done" {
				completed = true
			}
		case <-timer.C:
			t.Fatalf("installed speech lifecycle incomplete: started=%v completed=%v", started, completed)
		}
	}
	for _, text := range []string{"Synthetic follow-up one", "Synthetic follow-up two"} {
		request("thread/realtime/appendSpeech", map[string]any{"threadId": id, "text": text})
		select {
		case frame := <-frames:
			var value map[string]any
			if err := json.Unmarshal(frame, &value); err != nil {
				t.Fatal(err)
			}
			content, _ := value["content"].([]any)
			var body string
			if len(content) == 1 {
				block, _ := content[0].(map[string]any)
				body, _ = block["text"].(string)
			}
			if value["channel"] != "speakable" || body != text {
				t.Fatalf("follow-up did not reach the remote speech channel: %s", frame)
			}
		case <-ctx.Done():
			t.Fatal("follow-up speech frame timed out")
		}
		completed = false
		for !completed {
			select {
			case notification := <-notifications:
				params, _ := notification["params"].(map[string]any)
				completed = notification["method"] == "thread/realtime/transcript/done" && params["role"] == "assistant"
			case <-ctx.Done():
				t.Fatal("follow-up speech completion timed out")
			}
		}
		t.Logf("installed follow-up speech and completion received: %s", text)
	}
	request("thread/realtime/stop", map[string]any{"threadId": id})
}

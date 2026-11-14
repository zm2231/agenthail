package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

type whoamiModel struct {
	mu      sync.Mutex
	calls   int
	outputs []string
}

func (m *whoamiModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Input []struct {
			Type   string          `json:"type"`
			Output json.RawMessage `json:"output"`
		} `json:"input"`
	}
	body, _ := io.ReadAll(r.Body)
	_ = json.Unmarshal(body, &request)
	m.mu.Lock()
	m.calls++
	id := fmt.Sprintf("resp_%d", m.calls)
	answered := false
	for _, item := range request.Input {
		if item.Type == "function_call_output" {
			answered = true
			m.outputs = append(m.outputs, string(item.Output))
		}
	}
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	if answered {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}})
	} else {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id, "namespace": "mcp__agenthail", "name": "agenthail_whoami", "arguments": "{}"}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func (m *whoamiModel) output() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return strings.Join(m.outputs, "\n")
}

type appServer struct {
	ws   *websocket.Conn
	next int
}

func dialAppServer(t *testing.T, socket string) *appServer {
	t.Helper()
	dialer := websocket.Dialer{NetDialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}
	ws, _, err := dialer.Dial("ws://localhost/rpc", nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ws.Close() })
	server := &appServer{ws: ws}
	server.call(t, "initialize", map[string]any{"clientInfo": map[string]any{"name": "agenthail-test", "version": "1"}})
	if err := ws.WriteJSON(map[string]any{"method": "initialized"}); err != nil {
		t.Fatal(err)
	}
	return server
}

func (s *appServer) call(t *testing.T, method string, params map[string]any) map[string]any {
	t.Helper()
	s.next++
	id := s.next
	if err := s.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		t.Fatal(err)
	}
	_ = s.ws.SetReadDeadline(time.Now().Add(60 * time.Second))
	for {
		var message map[string]any
		if err := s.ws.ReadJSON(&message); err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		if got, ok := message["id"].(float64); ok && int(got) == id {
			if failure, ok := message["error"]; ok {
				t.Fatalf("%s: %v", method, failure)
			}
			result, _ := message["result"].(map[string]any)
			return result
		}
	}
}

func TestARealCodexThreadCallsTheToolsAsItselfThroughTheStdioRelay(t *testing.T) {
	d, reg, _, stop := mcpSocketDaemon(t)
	ready := filepath.Join(t.TempDir(), "ready")
	if err := os.WriteFile(ready, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	model := &whoamiModel{}
	relay := fmt.Sprintf("[mcp_servers.agenthail]\ncommand = %q\nargs = [\"-test.run=^TestMCPStdioRelayHelper$\"]\ndefault_tools_approval_mode = \"approve\"\nenv = { AGENTHAIL_TEST_RELAY_READY = %q, AGENTHAIL_TEST_RELAY_SOCKET = %q }\n", os.Args[0], ready, MCPSocketPath())
	real := codextest.StartRealWith(t, model, relay)
	stop()
	d.Surfaces = append(d.Surfaces, surfaces.NewCodexIn(surfaces.CodexHome{Codex: real.CodexHome, State: filepath.Join(real.Home, ".agenthail")}, ""))
	restarted, err := d.startMCPSocket()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(restarted)
	cwd := filepath.Join(real.Home, "work")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	server := dialAppServer(t, real.Socket())
	var threads []string
	for range 2 {
		started := server.call(t, "thread/start", map[string]any{"cwd": cwd})
		thread, _ := started["thread"].(map[string]any)
		id, _ := thread["id"].(string)
		if id == "" {
			t.Fatalf("thread/start answered %v", started)
		}
		registerCodex(t, reg, id, cwd)
		threads = append(threads, id)
	}
	for _, thread := range threads {
		server.call(t, "turn/start", map[string]any{"threadId": thread, "input": []any{map[string]any{"type": "text", "text": "who am I"}}})
	}
	for _, thread := range threads {
		testkit.Eventually(t, "the tool answered as "+thread, 60*time.Second, func() bool {
			return strings.Contains(model.output(), `\"sessionId\":\"`+thread+`\"`) || strings.Contains(model.output(), `"sessionId":"`+thread+`"`)
		})
	}
}

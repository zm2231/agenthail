package codextest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
)

var desktopRPCMethod = regexp.MustCompile(`request\("([^"]+)"`)

// Desktop is a fake Codex Desktop renderer reachable over the Chrome
// DevTools protocol. It answers hook installation itself and hands every
// app-server request to respond, keyed by JSON-RPC method.
type Desktop struct {
	URL         string
	mu          sync.Mutex
	methods     []string
	expressions []string
	lookups     int
}

// Lookups counts the debug target listings fetched from the renderer.
func (b *Desktop) Lookups() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lookups
}

func (b *Desktop) Methods() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.methods...)
}

func (b *Desktop) Request(method string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index, seen := range b.methods {
		if seen == method {
			return b.expressions[index]
		}
	}
	return ""
}

func StartDesktop(t testing.TB, respond func(method string) string) *Desktop {
	t.Helper()
	bridge := &Desktop{}
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		bridge.mu.Lock()
		bridge.lookups++
		bridge.mu.Unlock()
		json.NewEncoder(w).Encode([]map[string]any{{"type": "page", "url": "app://-/index.html", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var request map[string]any
			if conn.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			value := any("")
			switch match := desktopRPCMethod.FindStringSubmatch(expression); {
			case strings.Contains(expression, "electronBridge.sendMessageFromView"):
				value = "hooked"
			case match != nil:
				bridge.mu.Lock()
				bridge.methods = append(bridge.methods, match[1])
				bridge.expressions = append(bridge.expressions, expression)
				bridge.mu.Unlock()
				value = respond(match[1])
			case strings.Contains(expression, "__agenthailPayloads"):
				value = "ok"
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	t.Cleanup(server.Close)
	bridge.URL = server.URL
	return bridge
}

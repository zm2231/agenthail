package surfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

type fakeDesktopCDPServer struct {
	server  *httptest.Server
	mu      sync.Mutex
	methods []string
	model   string
}

func newFakeDesktopCDPServer(t *testing.T, model string) *fakeDesktopCDPServer {
	t.Helper()
	fake := &fakeDesktopCDPServer{model: model}
	upgrader := websocket.Upgrader{}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode([]map[string]any{{
			"type":                 "page",
			"url":                  "app://-/index.html",
			"webSocketDebuggerUrl": "ws" + strings.TrimPrefix(fake.server.URL, "http") + "/ws",
		}})
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
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
			switch {
			case strings.Contains(expression, "electronBridge.sendMessageFromView"):
				value = "hooked"
			case strings.Contains(expression, `"thread/read"`):
				fake.mu.Lock()
				fake.methods = append(fake.methods, "thread/read")
				fake.mu.Unlock()
				value = `{"result":{"thread":{"id":"thread","model":"` + fake.model + `"}}}`
			case strings.Contains(expression, `"thread/resume"`):
				fake.mu.Lock()
				fake.methods = append(fake.methods, "thread/resume")
				fake.mu.Unlock()
				value = `{"result":{"model":"resumed-model"}}`
			}
			_ = conn.WriteJSON(map[string]any{
				"id": request["id"],
				"result": map[string]any{
					"result": map[string]any{"value": value},
				},
			})
		}
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (f *fakeDesktopCDPServer) calledMethods() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.methods...)
}

func TestCodexModelReadUsesMetadataWithoutResuming(t *testing.T) {
	fake := newFakeDesktopCDPServer(t, "metadata-model")
	codex := NewCodex(fake.server.URL)
	model, err := codex.Model(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop}, "")
	if err != nil {
		t.Fatal(err)
	}
	if model != "metadata-model" {
		t.Fatalf("model=%q, want metadata-model", model)
	}
	if got := fake.calledMethods(); len(got) != 1 || got[0] != "thread/read" {
		t.Fatalf("RPC methods=%v, want only thread/read", got)
	}
}

func TestCodexModelReadMissingMetadataIsTypedFailure(t *testing.T) {
	fake := newFakeDesktopCDPServer(t, "")
	codex := NewCodex(fake.server.URL)
	_, err := codex.Model(context.Background(), &surface.Session{ID: "thread", Transport: codexTransportDesktop}, "")
	if err == nil {
		t.Fatal("missing model metadata was not reported as a failure")
	}
	if got := fake.calledMethods(); len(got) != 1 || got[0] != "thread/read" {
		t.Fatalf("RPC methods=%v, want only thread/read", got)
	}
}

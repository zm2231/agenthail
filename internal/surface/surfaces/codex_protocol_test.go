package surfaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

var desktopRPCMethod = regexp.MustCompile(`request\("([^"]+)"`)

// desktopBridge is a fake Codex Desktop renderer reachable over the Chrome
// DevTools protocol. It answers hook installation itself and hands every
// app-server request to respond, keyed by JSON-RPC method.
type desktopBridge struct {
	URL         string
	mu          sync.Mutex
	methods     []string
	expressions []string
}

func (b *desktopBridge) Methods() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]string(nil), b.methods...)
}

func (b *desktopBridge) Request(method string) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	for index, seen := range b.methods {
		if seen == method {
			return b.expressions[index]
		}
	}
	return ""
}

func startDesktopBridge(t *testing.T, respond func(method string) string) *desktopBridge {
	t.Helper()
	bridge := &desktopBridge{}
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
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

func startRendererDesktopBridge(t *testing.T) string {
	t.Helper()
	return startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/read":
			return `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"}}}}`
		case "thread/loaded/list", "thread/items/list":
			return `{"result":{"data":[]}}`
		case "thread/turns/list":
			return `{"result":{"data":[{"id":"completed","status":{"type":"completed"}}]}}`
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","name":"Desktop conversation","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"},"method":"turn/start"}}`
		}
		return ""
	}).URL
}

func TestCodexStartSessionPrefersDesktopOwner(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	bridge := startDesktopBridge(t, func(method string) string {
		switch method {
		case "thread/start":
			return `{"result":{"cwd":"/tmp/project","thread":{"id":"desktop-new","name":"","source":"vscode"}}}`
		case "turn/start":
			return `{"result":{"turn":{"id":"desktop-turn"}}}`
		}
		return ""
	})
	session, sent, err := NewCodex(bridge.URL).StartSession(context.Background(), surface.SessionStartOptions{Message: "Build the release", Cwd: "/tmp/project", Model: "gpt-5.6-sol", ApprovalPolicy: "on-request", Owner: codexTransportDesktop})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "desktop-new" || session.Transport != codexTransportDesktop || session.Status != surface.StatusBusy || session.Name != "Build the release" || sent.UUID != "desktop-turn" || !sent.Accepted {
		t.Fatalf("session=%+v sent=%+v", session, sent)
	}
	for _, field := range []string{`"threadSource":"agenthail"`, `"serviceName":"agenthail"`, `"cwd":"/tmp/project"`, `"model":"gpt-5.6-sol"`, `"approvalPolicy":"on-request"`} {
		if !strings.Contains(bridge.Request("thread/start"), field) {
			t.Fatalf("thread/start missing %s: %s", field, bridge.Request("thread/start"))
		}
	}
	if !strings.Contains(bridge.Request("turn/start"), `"threadId":"desktop-new"`) {
		t.Fatalf("turn/start not bound to the created thread: %s", bridge.Request("turn/start"))
	}
}

func TestCodexDesktopDiscoveryNeverBootstrapsManagedRuntime(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "managed-runtime.log")
	script := filepath.Join(root, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	t.Setenv("CODEX_HOME", filepath.Join(root, "missing-codex-home"))
	if _, err := NewCodex(startRendererDesktopBridge(t)).List(context.Background()); err != nil {
		t.Fatal(err)
	}
	if output, _ := os.ReadFile(logPath); len(output) != 0 {
		t.Fatalf("Desktop discovery bootstrapped managed runtime: %s", output)
	}
}

func TestCodexResolveExactNameUsesHistorySearch(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	searchCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
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
			var value any = ""
			switch {
			case strings.Contains(expression, "electronBridge.sendMessageFromView"):
				value = "already"
			case strings.Contains(expression, `"thread/search"`):
				searchCalls++
				value = `{"result":{"data":[{"thread":{"id":"thread-1","name":"Q","cwd":"/tmp","source":"vscode","status":{"type":"idle"}},"snippet":"test"}]}}`
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	session, err := NewCodex(server.URL).Resolve(context.Background(), "Q")
	if err != nil || session == nil || session.ID != "thread-1" || session.Transport != codexTransportDesktop || searchCalls != 1 {
		t.Fatalf("session=%+v search_calls=%d err=%v", session, searchCalls, err)
	}
}

func TestCodexResolveIDReadsThreadWithoutListing(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	readCalls := 0
	listCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
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
			switch {
			case strings.Contains(expression, "electronBridge.sendMessageFromView"):
				value = "already"
			case strings.Contains(expression, `"thread/loaded/list"`):
				value = `{"result":{"data":[]}}`
			case strings.Contains(expression, `"thread/read"`):
				readCalls++
				value = `{"result":{"thread":{"id":"019f004a-a94e-7313-a599-2db587a1f67a","name":"known","cwd":"/tmp","source":"vscode","status":{"type":"idle"}}}}`
			case strings.Contains(expression, `"thread/list"`):
				listCalls++
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	session, err := NewCodex(server.URL).Resolve(context.Background(), "019f004a-a94e-7313-a599-2db587a1f67a")
	if err != nil || session == nil || session.ID != "019f004a-a94e-7313-a599-2db587a1f67a" || session.Transport != codexTransportDesktop || readCalls != 1 || listCalls != 0 {
		t.Fatalf("session=%+v read_calls=%d list_calls=%d err=%v", session, readCalls, listCalls, err)
	}
}

func TestCodexEnsureWritableRefreshesDesktopSourceTransport(t *testing.T) {
	codex := NewCodex(startRendererDesktopBridge(t))
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "agenthail", Transport: codexTransportReadOnly}
	if err := codex.EnsureWritable(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	if session.Transport != codexTransportDesktop {
		t.Fatalf("transport=%q", session.Transport)
	}
}

func TestCodexDesktopSendNeverResumesLoadedThreadAndLoadsUnloadedThreadOnce(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	ready := `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"idle"},"canAcceptDirectInput":true,"model":"gpt-5.6-terra"}}}`
	for _, test := range []struct {
		name       string
		firstRead  string
		session    surface.Session
		wantResume int
	}{
		{
			name:      "loaded thread behind stale managed transport",
			firstRead: ready,
			session:   surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "agenthail", Transport: codexTransportManaged},
		},
		{
			name:       "unloaded Desktop thread",
			firstRead:  `{"result":{"thread":{"id":"thread","source":"vscode","status":{"type":"notLoaded"}}}}`,
			session:    surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "vscode", Transport: codexTransportDesktop},
			wantResume: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resumed := false
			bridge := startDesktopBridge(t, func(method string) string {
				switch method {
				case "thread/read":
					if resumed || test.wantResume == 0 {
						return ready
					}
					return test.firstRead
				case "thread/resume":
					if test.wantResume == 0 {
						return `{"error":{"code":-1,"message":"must not resume a loaded Desktop thread"}}`
					}
					resumed = true
					return `{"result":{"thread":{"id":"thread"}}}`
				case "thread/turns/list":
					return `{"result":{"data":[]}}`
				case "turn/start":
					return `{"result":{"turn":{"id":"turn"}}}`
				}
				return ""
			})
			session := test.session
			sent, err := NewCodex(bridge.URL).SendWithOptions(context.Background(), &session, "deliver queued work", surface.SendOptions{TurnOptions: surface.TurnOptions{Mode: "plan"}})
			if err != nil || sent == nil || !sent.Accepted || sent.UUID != "turn" {
				t.Fatalf("sent=%+v err=%v", sent, err)
			}
			if session.Source != "vscode" || session.Transport != codexTransportDesktop {
				t.Fatalf("session=%+v", session)
			}
			methods := bridge.Methods()
			resumes, starts, resumeAt, startAt := 0, 0, -1, -1
			for index, method := range methods {
				switch method {
				case "thread/resume":
					resumes++
					resumeAt = index
				case "turn/start":
					starts++
					startAt = index
				}
			}
			if resumes != test.wantResume || starts != 1 || resumeAt > startAt {
				t.Fatalf("methods=%v", methods)
			}
		})
	}
}

func TestCodexObserveRepairsStaleManagedTransport(t *testing.T) {
	codex := NewCodex(startRendererDesktopBridge(t))
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "agenthail", Transport: codexTransportManaged}
	observation, err := codex.Observe(context.Background(), session)
	if err != nil || observation == nil || observation.Status != surface.StatusIdle {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if session.Source != "vscode" || session.Transport != codexTransportDesktop {
		t.Fatalf("session=%+v", session)
	}
}

func TestCodexResolveRejectsDuplicateExactNamesAcrossPages(t *testing.T) {
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	searchCalls := 0
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
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
			var value any = ""
			switch {
			case strings.Contains(expression, "electronBridge.sendMessageFromView"):
				value = "already"
			case strings.Contains(expression, `"thread/search"`):
				searchCalls++
				value = `{"result":{"data":[{"thread":{"id":"thread-1","name":"duplicate","cwd":"/one"}},{"thread":{"id":"thread-2","name":"DUPLICATE","cwd":"/two"}}]}}`
			}
			_ = conn.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()

	_, err := NewCodex(server.URL).Resolve(context.Background(), "duplicate")
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "thread-1") || !strings.Contains(err.Error(), "thread-2") {
		t.Fatalf("err=%v", err)
	}
}

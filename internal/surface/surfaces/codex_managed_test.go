package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexCatalogListIsNotComplete(t *testing.T) {
	if NewCodex("").CatalogListComplete() {
		t.Fatal("Codex List is bounded and must not reconcile omissions")
	}
}

func TestCodexSearchFallsBackToManagedRuntime(t *testing.T) {
	home := startManagedCodex(t, managedDiscovery).Home
	logPath := filepath.Join(home, "managed-runtime.log")
	script := filepath.Join(home, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	upgrader := websocket.Upgrader{}
	var server *httptest.Server
	handler := http.NewServeMux()
	handler.HandleFunc("/json", func(w http.ResponseWriter, _ *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{{"type": "node", "webSocketDebuggerUrl": "ws" + strings.TrimPrefix(server.URL, "http") + "/ws"}})
	})
	handler.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			var request map[string]any
			if connection.ReadJSON(&request) != nil {
				return
			}
			params, _ := request["params"].(map[string]any)
			expression, _ := params["expression"].(string)
			value := ""
			switch {
			case strings.Contains(expression, "process._getActiveHandles"):
				value = "already"
			case strings.Contains(expression, `"thread/loaded/list"`):
				value = `{"result":{"data":[]}}`
			case strings.Contains(expression, `"thread/search"`):
				value = `{"error":{"code":-32601,"message":"method not found"}}`
			}
			_ = connection.WriteJSON(map[string]any{"id": request["id"], "result": map[string]any{"result": map[string]any{"value": value}}})
		}
	})
	server = httptest.NewServer(handler)
	defer server.Close()
	codex := NewCodex("")
	codex.desktopURL = server.URL
	results, err := codex.SearchSessions(context.Background(), "managed", 10)
	if err != nil || len(results) != 1 || results[0].Session.Transport != codexTransportManaged {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	if output, _ := os.ReadFile(logPath); len(output) != 0 {
		t.Fatalf("history search bootstrapped managed runtime: %s", output)
	}
}

func TestCodexDiscoveryDoesNotStartManagedRuntime(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	logPath := filepath.Join(home, "managed-runtime.log")
	script := filepath.Join(home, "codex")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nexit 1\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	codex := NewCodex("")
	codex.desktopURL = "http://127.0.0.1:1"
	if err := codex.Ready(context.Background()); err == nil {
		t.Fatal("Ready() succeeded without any available Codex transport")
	}
	if output, _ := os.ReadFile(logPath); len(output) != 0 {
		t.Fatalf("discovery started managed runtime: %s", output)
	}
}

func TestCodexRejectsMutationsForPlainTerminalSession(t *testing.T) {
	codex := NewCodex("http://127.0.0.1:1")
	session := &surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "cli", Transport: codexTransportReadOnly}
	_, err := codex.Send(context.Background(), session, "do not deliver")
	if err == nil || !strings.Contains(err.Error(), "read only") {
		t.Fatalf("err=%v", err)
	}
}

func TestReadOnlySessionCoversLegacyRowsWithoutBlockingDesktop(t *testing.T) {
	legacy := &surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle}
	unclassifiedDesktopSource := &surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode"}
	desktop := &surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "managed"}
	if !surface.IsReadOnlySession(legacy) {
		t.Fatal("legacy unloaded session was writable")
	}
	if !surface.IsReadOnlySession(unclassifiedDesktopSource) {
		t.Fatal("blank transport was writable")
	}
	if surface.IsReadOnlySession(desktop) {
		t.Fatal("Desktop session was read only")
	}
	unbridgedDesktop := &surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "readOnly"}
	if reason := surface.ReadOnlySessionReason(unbridgedDesktop); reason != "Codex Desktop is not available through Agenthail's Desktop bridge; quit Codex and run 'agenthail launch codex'" {
		t.Fatalf("reason=%q", reason)
	}
}

func TestCodexManagedRuntimeStatusReportsDurability(t *testing.T) {
	for _, test := range []struct {
		name, backend, supervisor, xpc string
		durable, remediation           bool
	}{
		{name: "unsupervised pid backend", backend: "pid", remediation: true},
		{name: "homebrew-supervised pid backend", backend: "pid", supervisor: "homebrew", durable: true},
		{name: "launchd-supervised pid backend", backend: "pid", xpc: "com.agenthail.daemon", durable: true},
		{name: "unknown supervisor", backend: "pid", supervisor: "unknown"},
		{name: "launchd backend", backend: "launchd", durable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			script := filepath.Join(t.TempDir(), "codex")
			body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"" + test.backend + "\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
			if err := os.WriteFile(script, []byte(body), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("AGENTHAIL_CODEX_BIN", script)
			t.Setenv("AGENTHAIL_DAEMON_SUPERVISOR", test.supervisor)
			t.Setenv("XPC_SERVICE_NAME", test.xpc)
			status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
			if !status.Reachable || status.Durable != test.durable || status.Backend != test.backend {
				t.Fatalf("status=%+v", status)
			}
			if test.remediation {
				if !strings.Contains(status.Remediation, "agenthail launch codex") {
					t.Fatalf("remediation=%q", status.Remediation)
				}
			} else if test.durable && (status.Detail != "" || status.Remediation != "") {
				t.Fatalf("supervised status carries degraded detail: %+v", status)
			}
		})
	}
}

func TestCodexRuntimeStatusPrefersReachableDesktopBridge(t *testing.T) {
	status := NewCodex(startRendererDesktopBridge(t)).RuntimeStatus(context.Background())
	if !status.Reachable || !status.Durable || status.Backend != "desktop" || status.Name != "Codex Desktop bridge" {
		t.Fatalf("status=%+v", status)
	}
	if !strings.Contains(status.Detail, "read path") || !strings.Contains(status.Detail, "per target") {
		t.Fatalf("status=%+v", status)
	}
}

func TestCodexEnsureRuntimeStartsMissingManagedDaemonOnce(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls.log")
	codeHome := filepath.Join(root, "codex-home")
	socketPath := filepath.Join(codeHome, "app-server-control", "app-server-control.sock")
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nif [ \"$1 $2 $3\" = \"app-server daemon enable-remote-control\" ]; then exit 0; fi\nif [ \"$1 $2 $3\" = \"app-server daemon start\" ]; then mkdir -p \"$(dirname \"$AGENTHAIL_TEST_SOCKET\")\"; : > \"$AGENTHAIL_TEST_SOCKET\"; exit 0; fi\nexit 2\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	t.Setenv("AGENTHAIL_TEST_SOCKET", socketPath)
	t.Setenv("CODEX_HOME", codeHome)
	codex := NewCodex("")
	if err := codex.EnsureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := codex.EnsureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(data), "app-server daemon start"); got != 1 {
		t.Fatalf("start calls=%d log=%q", got, data)
	}
}

func TestCodexEnsureRuntimeNamesMissingManagedDaemon(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_BIN", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("CODEX_HOME", t.TempDir())
	err := NewCodex("").EnsureRuntime(context.Background())
	if err == nil || !strings.Contains(err.Error(), "remote control") || !strings.Contains(err.Error(), "AGENTHAIL_CODEX_BIN") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexEnsureRuntimeEnablesRemoteControlWhenDisabled(t *testing.T) {
	root := t.TempDir()
	codeHome := filepath.Join(root, "codex-home")
	socketPath := filepath.Join(codeHome, "app-server-control", "app-server-control.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, nil, 0600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(codeHome, "app-server-daemon", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settingsPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, []byte(`{"remoteControlEnabled":false}`), 0600); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "calls.log")
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nexit 0\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_TEST_LOG", logPath)
	t.Setenv("CODEX_HOME", codeHome)
	if err := NewCodex("").EnsureRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || strings.TrimSpace(string(data)) != "app-server daemon enable-remote-control" {
		t.Fatalf("log=%q err=%v", data, err)
	}
}

func isolatedManagedRuntime(t *testing.T) *Codex {
	t.Helper()
	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)
	codex := NewCodex(server.URL)
	codex.managed = true
	return codex
}

const managedThreadID = "019f004a-0000-7000-8000-00000000beef"

// managedCodex is a fake managed Codex app-server listening on the
// $CODEX_HOME control socket. Every JSON-RPC request is recorded and answered
// by respond; notifications queued with Notify (also from inside respond) are
// delivered ahead of the next thread/read response, the way the app-server
// interleaves them.
type managedCodex struct {
	Home    string
	mu      sync.Mutex
	calls   []managedCall
	pending []map[string]any
}

type managedCall struct {
	Method string
	Params map[string]any
}

func (m *managedCodex) Notify(method string, params map[string]any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pending = append(m.pending, map[string]any{"method": method, "params": params})
}

func (m *managedCodex) Calls(method string) []map[string]any {
	m.mu.Lock()
	defer m.mu.Unlock()
	var params []map[string]any
	for _, call := range m.calls {
		if call.Method == method {
			params = append(params, call.Params)
		}
	}
	return params
}

func (m *managedCodex) Methods() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	methods := make([]string, 0, len(m.calls))
	for _, call := range m.calls {
		methods = append(methods, call.Method)
	}
	return methods
}

func startManagedCodex(t *testing.T, respond func(method string, params map[string]any) map[string]any) *managedCodex {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	for _, dir := range []string{"app-server-control", "app-server-daemon"} {
		if err := os.MkdirAll(filepath.Join(home, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "app-server-daemon", "settings.json"), []byte(`{"remoteControlEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(home, "app-server-control", "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
	fake := &managedCodex{Home: home}
	upgrader := websocket.Upgrader{}
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer connection.Close()
		for {
			var request map[string]any
			if connection.ReadJSON(&request) != nil {
				return
			}
			id, hasID := request["id"]
			if !hasID {
				continue
			}
			method, _ := request["method"].(string)
			params, _ := request["params"].(map[string]any)
			fake.mu.Lock()
			fake.calls = append(fake.calls, managedCall{Method: method, Params: params})
			fake.mu.Unlock()
			result := respond(method, params)
			var notifications []map[string]any
			if method == "thread/read" {
				fake.mu.Lock()
				notifications, fake.pending = fake.pending, nil
				fake.mu.Unlock()
			}
			for _, notification := range notifications {
				_ = connection.WriteJSON(notification)
			}
			response := map[string]any{"id": id, "result": map[string]any{}}
			if result != nil {
				if rpcError, failed := result["error"]; failed {
					response = map[string]any{"id": id, "error": rpcError}
				} else {
					response["result"] = result
				}
			}
			_ = connection.WriteJSON(response)
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() { _ = server.Close() })
	return fake
}

func managedDiscovery(method string, _ map[string]any) map[string]any {
	thread := map[string]any{"id": managedThreadID, "name": "managed", "source": "agenthail", "status": map[string]any{"type": "idle"}}
	switch method {
	case "thread/loaded/list":
		return map[string]any{"data": []any{managedThreadID}}
	case "thread/read":
		return map[string]any{"thread": thread}
	case "thread/search":
		return map[string]any{"data": []any{map[string]any{"thread": thread}}}
	}
	return nil
}

func managedSession() *surface.Session {
	return &surface.Session{ID: "thread", Surface: surface.KindCodex, Source: "agenthail", Transport: codexTransportManaged}
}

func TestCodexResolveIDKeepsManagedLoadedSessionWritable(t *testing.T) {
	startManagedCodex(t, managedDiscovery)
	session, err := isolatedManagedRuntime(t).Resolve(context.Background(), managedThreadID)
	if err != nil || session == nil || session.ID != managedThreadID || session.Transport != codexTransportManaged {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestPrepareManagedTerminalSessionUsesProviderThreadIdentity(t *testing.T) {
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/start" {
			return map[string]any{"cwd": "/tmp/project", "thread": map[string]any{"id": "thread-new", "source": "appServer"}}
		}
		return nil
	})
	session, err := isolatedManagedRuntime(t).PrepareManagedTerminalSession(context.Background(), "/tmp/project", "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "thread-new" || session.Surface != surface.KindCodex || session.Transport != codexTransportManaged || session.Cwd != "/tmp/project" {
		t.Fatalf("session=%+v", session)
	}
	if starts := fake.Calls("thread/start"); len(starts) != 1 || len(fake.Calls("turn/start")) != 0 {
		t.Fatalf("methods=%v", fake.Methods())
	} else if params := starts[0]; params["threadSource"] != "agenthail-terminal" || params["serviceName"] != "agenthail" || params["cwd"] != "/tmp/project" || params["model"] != "gpt-5.6-sol" {
		t.Fatalf("thread params=%v", params)
	}
}

func managedTurn(id, status string, items ...map[string]any) map[string]any {
	turn := map[string]any{"id": id, "status": status}
	if len(items) > 0 {
		values := make([]any, len(items))
		for index, item := range items {
			values[index] = item
		}
		turn["items"] = values
	}
	return map[string]any{"thread": map[string]any{"id": "thread", "turns": []any{turn}}}
}

func agentMessage(id, phase, text string) map[string]any {
	return map[string]any{"id": id, "type": "agentMessage", "phase": phase, "text": text}
}

// managedThreadReads answers thread/read with reads in order, repeating the last.
func managedThreadReads(reads ...map[string]any) func(string, map[string]any) map[string]any {
	var mu sync.Mutex
	index := 0
	return func(method string, _ map[string]any) map[string]any {
		if method != "thread/read" {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		read := reads[min(index, len(reads)-1)]
		index++
		return read
	}
}

func TestCodexManagedStreamEmitsAuthoritativeTurnEvents(t *testing.T) {
	for _, test := range []struct {
		name   string
		turnID string
		reads  []map[string]any
		check  func(*testing.T, []surface.StreamEvent, error)
	}{
		{
			name: "waits for a new turn instead of replaying history",
			reads: []map[string]any{
				managedTurn("old", "completed", agentMessage("old", "", "old answer")),
				managedTurn("old", "completed", agentMessage("old", "", "old answer")),
				managedTurn("new", "inProgress", agentMessage("interim", "commentary", "hel")),
				managedTurn("new", "completed", agentMessage("interim", "commentary", "hel"), agentMessage("final", "final_answer", "hello")),
			},
			check: func(t *testing.T, events []surface.StreamEvent, err error) {
				if err != nil || len(events) != 3 || events[0].Text != "hel" || events[0].ID != "managed:new:text" || events[1].Text != "hello" || !events[1].Final || events[1].Operation != "upsert" || events[1].ID != "managed:new:text" || events[2].Kind != "done" || events[2].ID != "managed:new:done" {
					t.Fatalf("err=%v events=%+v", err, events)
				}
			},
		},
		{
			name:   "completed target emits only the latest final answer",
			turnID: "done",
			reads: []map[string]any{
				managedTurn("done", "completed", agentMessage("interim", "commentary", "checking"), agentMessage("final", "final_answer", "finish"), agentMessage("final", "final_answer", "finished")),
			},
			check: func(t *testing.T, events []surface.StreamEvent, err error) {
				if err != nil || len(events) != 2 || events[0].Text != "finished" || !events[0].Final || events[1].Kind != "done" {
					t.Fatalf("err=%v events=%+v", err, events)
				}
			},
		},
		{
			name: "growing final_answer phase stays partial until the turn ends",
			reads: []map[string]any{
				managedTurn("old", "completed", agentMessage("old", "", "old answer")),
				managedTurn("old", "completed", agentMessage("old", "", "old answer")),
				managedTurn("new", "inProgress", agentMessage("answer", "final_answer", "hel")),
				managedTurn("new", "inProgress", agentMessage("answer", "final_answer", "hello")),
				managedTurn("new", "completed", agentMessage("answer", "final_answer", "hello")),
			},
			check: func(t *testing.T, events []surface.StreamEvent, err error) {
				if err != nil || len(events) != 4 || events[0].Text != "hel" || events[0].Final || events[1].Text != "hello" || events[1].Final || events[1].Operation != "upsert" || events[2].Text != "hello" || !events[2].Final || events[3].Kind != "done" {
					t.Fatalf("err=%v events=%+v", err, events)
				}
			},
		},
		{
			name:   "failed turn reports an error and no final answer",
			turnID: "failed",
			reads: []map[string]any{
				managedTurn("failed", "failed", agentMessage("answer", "final_answer", "not success")),
			},
			check: func(t *testing.T, events []surface.StreamEvent, err error) {
				if err == nil || len(events) != 0 {
					t.Fatalf("err=%v events=%+v", err, events)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			startManagedCodex(t, managedThreadReads(test.reads...))
			var events []surface.StreamEvent
			err := isolatedManagedRuntime(t).Stream(context.Background(), managedSession(), test.turnID, func(event surface.StreamEvent) {
				events = append(events, event)
			}, 2*time.Second)
			test.check(t, events, err)
		})
	}
}

func TestManagedStreamRestartsEmitIdempotentAuthoritativeSnapshots(t *testing.T) {
	var events []surface.StreamEvent
	for _, body := range []string{"hello", "hello", "hello world"} {
		startManagedCodex(t, managedThreadReads(managedTurn("turn-1", "inProgress", agentMessage("answer", "", body))))
		err := isolatedManagedRuntime(t).Stream(context.Background(), managedSession(), "", func(event surface.StreamEvent) {
			events = append(events, event)
		}, 20*time.Millisecond)
		if !errors.Is(err, surface.ErrStreamWindow) {
			t.Fatalf("window err=%v", err)
		}
	}
	if len(events) != 3 {
		t.Fatalf("events=%+v", events)
	}
	for index, want := range []string{"hello", "hello", "hello world"} {
		event := events[index]
		if event.ID != "managed:turn-1:text" || event.Operation != "upsert" || event.Text != want || event.Version != uint64(len(want)) || event.Kind == "done" {
			t.Fatalf("event %d=%+v", index, event)
		}
	}
}

func TestCodexManagedStreamForwardsGoalAndContextNotificationsForItsThread(t *testing.T) {
	fake := startManagedCodex(t, managedThreadReads(managedTurn("turn", "completed", agentMessage("final", "final_answer", "done"))))
	fake.Notify("thread/goal/cleared", map[string]any{"threadId": "other"})
	fake.Notify("thread/goal/cleared", map[string]any{"threadId": "thread"})
	fake.Notify("thread/tokenUsage/updated", map[string]any{
		"threadId": "thread",
		"tokenUsage": map[string]any{
			"last":               map[string]any{"inputTokens": float64(90000), "cachedInputTokens": float64(80000), "outputTokens": float64(1000), "reasoningOutputTokens": float64(200), "totalTokens": float64(91000)},
			"total":              map[string]any{"totalTokens": float64(1000000)},
			"modelContextWindow": float64(258400),
		},
	})
	fake.Notify("item/started", map[string]any{"threadId": "thread", "item": map[string]any{"type": "contextCompaction"}})
	fake.Notify("item/completed", map[string]any{"threadId": "thread", "item": map[string]any{"type": "contextCompaction"}})
	var events []surface.StreamEvent
	if err := isolatedManagedRuntime(t).Stream(context.Background(), managedSession(), "turn", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 2*time.Second); err != nil {
		t.Fatal(err)
	}
	if len(events) != 6 || events[4].Kind != "text" || !events[4].Final || events[5].Kind != "done" {
		t.Fatalf("events=%+v", events)
	}
	goal, usage, started, completed := events[0], events[1], events[2], events[3]
	if goal.Kind != "goal" || goal.Operation != "replace" || goal.Goal != nil {
		t.Fatalf("goal=%+v", goal)
	}
	if usage.Kind != "context" || usage.Context == nil || usage.Context.UsedTokens != 91000 || usage.Context.ContextWindow != 258400 || usage.Context.CumulativeTokens != 1000000 {
		t.Fatalf("usage=%+v", usage)
	}
	if started.Context == nil || !started.Context.Compacting || completed.Context == nil || completed.Context.Compacting {
		t.Fatalf("compaction started=%+v completed=%+v", started, completed)
	}
}

func TestCodexManagedInterruptTargetsOnlyTheConfirmedTurn(t *testing.T) {
	for _, test := range []struct {
		name      string
		active    string
		interrupt bool
	}{
		{name: "replacement turn is never interrupted", active: "turn-b"},
		{name: "confirmed turn is interrupted", active: "turn-a", interrupt: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
				switch method {
				case "thread/resume":
					return map[string]any{"thread": map[string]any{"id": "thread"}}
				case "thread/turns/list":
					return map[string]any{"data": []any{map[string]any{"id": test.active, "status": map[string]any{"type": "inProgress"}}}}
				}
				return nil
			})
			err := isolatedManagedRuntime(t).InterruptTurn(context.Background(), managedSession(), "turn-a")
			interrupts := fake.Calls("turn/interrupt")
			if !test.interrupt {
				if err == nil || len(interrupts) != 0 {
					t.Fatalf("err=%v interrupts=%v", err, interrupts)
				}
				return
			}
			if err != nil || len(interrupts) != 1 || interrupts[0]["threadId"] != "thread" || interrupts[0]["turnId"] != "turn-a" {
				t.Fatalf("err=%v interrupts=%v", err, interrupts)
			}
		})
	}
}

func TestCodexManagedSendEncodesTurnOptionsWithActiveModel(t *testing.T) {
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		switch method {
		case "thread/resume":
			return map[string]any{"thread": map[string]any{"id": "thread"}}
		case "thread/read":
			return map[string]any{"thread": map[string]any{"model": "active-model"}}
		case "turn/start":
			return map[string]any{"turn": map[string]any{"id": "turn"}}
		}
		return nil
	})
	options := surface.TurnOptions{Effort: "high", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}
	sent, err := isolatedManagedRuntime(t).SendWithOptions(context.Background(), managedSession(), "hello", surface.SendOptions{TurnOptions: options})
	if err != nil || sent == nil || !sent.Accepted || sent.UUID != "turn" {
		t.Fatalf("sent=%+v err=%v", sent, err)
	}
	starts := fake.Calls("turn/start")
	if len(starts) != 1 {
		t.Fatalf("methods=%v", fake.Methods())
	}
	params := starts[0]
	mode, _ := params["collaborationMode"].(map[string]any)
	settings, _ := mode["settings"].(map[string]any)
	if params["threadId"] != "thread" || settings["model"] != "active-model" || settings["reasoning_effort"] != "high" || mode["mode"] != "plan" || params["serviceTierForTurn"] != "fast" {
		t.Fatal(params)
	}
	if schema, _ := params["outputSchema"].(map[string]any); schema["type"] != "object" {
		t.Fatalf("schema was not sent as JSON: %#v", params["outputSchema"])
	}
}

func TestCodexManagedObserveMapsTurnsToObservation(t *testing.T) {
	type turn struct {
		id, status string
		items      []any
	}
	user := func(text string) any { return map[string]any{"type": "userMessage", "text": text} }
	agent := func(text string) any { return map[string]any{"type": "agentMessage", "text": text} }
	for _, test := range []struct {
		name   string
		turns  []turn // newest first, as thread/turns/list returns them
		inline bool   // items embedded in thread/turns/list instead of fetched per turn
		check  func(*testing.T, *surface.TurnObservation)
	}{
		{
			name:  "active turn and latest completed reply",
			turns: []turn{{"running", "inProgress", []any{user("two"), agent("partial")}}, {"done", "completed", []any{user("one"), agent("same")}}},
			check: func(t *testing.T, observation *surface.TurnObservation) {
				if observation.Status != surface.StatusBusy || observation.ActiveTurnID != "running" || observation.CompletedTurnID != "done" || observation.Reply == nil || observation.Reply.Text != "same" {
					t.Fatalf("observation=%+v", observation)
				}
			},
		},
		{
			name:   "empty terminal turn is not the reply",
			turns:  []turn{{"empty", "completed", nil}, {"answer", "completed", []any{agent("actual reply")}}},
			inline: true,
			check: func(t *testing.T, observation *surface.TurnObservation) {
				if observation.TerminalTurnID != "empty" || observation.CompletedTurnID != "answer" || observation.Reply == nil || observation.Reply.Text != "actual reply" {
					t.Fatalf("observation=%+v", observation)
				}
			},
		},
		{
			name:  "failed turn completes with an explicit error",
			turns: []turn{{"failed", "failed", []any{agent("partial")}}},
			check: func(t *testing.T, observation *surface.TurnObservation) {
				if observation.Status != surface.StatusIdle || observation.CompletedTurnID != "failed" || observation.Reply == nil || !observation.Reply.Done || observation.Reply.Error == "" {
					t.Fatalf("observation=%+v", observation)
				}
			},
		},
		{
			name:  "system error turn is terminal",
			turns: []turn{{"broken", "systemError", nil}},
			check: func(t *testing.T, observation *surface.TurnObservation) {
				if observation.Status != surface.StatusIdle || observation.CompletedTurnID != "broken" || observation.Reply == nil || observation.Reply.Error == "" {
					t.Fatalf("observation=%+v", observation)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			startManagedCodex(t, func(method string, params map[string]any) map[string]any {
				switch method {
				case "thread/turns/list":
					data := make([]any, len(test.turns))
					for index, turn := range test.turns {
						entry := map[string]any{"id": turn.id, "status": map[string]any{"type": turn.status}}
						if test.inline {
							entry["items"] = turn.items
						}
						data[index] = entry
					}
					return map[string]any{"data": data}
				case "thread/items/list":
					for _, turn := range test.turns {
						if turn.id == params["turnId"] {
							data := make([]any, len(turn.items))
							for index, item := range turn.items {
								data[index] = map[string]any{"item": item}
							}
							return map[string]any{"data": data}
						}
					}
				}
				return nil
			})
			observation, err := isolatedManagedRuntime(t).Observe(context.Background(), managedSession())
			if err != nil {
				t.Fatal(err)
			}
			test.check(t, observation)
		})
	}
}

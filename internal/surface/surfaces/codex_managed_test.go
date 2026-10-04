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

func startManagedCodexFixture(t *testing.T) string {
	t.Helper()
	home, err := os.MkdirTemp("/tmp", "ah-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	socketDir := filepath.Join(home, "app-server-control")
	if err := os.MkdirAll(socketDir, 0700); err != nil {
		t.Fatal(err)
	}
	settingsDir := filepath.Join(home, "app-server-daemon")
	if err := os.MkdirAll(settingsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(settingsDir, "settings.json"), []byte(`{"remoteControlEnabled":true}`), 0600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("unix", filepath.Join(socketDir, "app-server-control.sock"))
	if err != nil {
		t.Fatal(err)
	}
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
			result := map[string]any{}
			switch method {
			case "thread/loaded/list":
				result["data"] = []any{"managed-thread"}
			case "thread/read":
				result["thread"] = map[string]any{"id": "managed-thread", "name": "managed", "source": "agenthail", "status": map[string]any{"type": "idle"}}
			case "thread/search":
				result["data"] = []any{map[string]any{"thread": map[string]any{"id": "managed-thread", "name": "managed", "source": "agenthail", "status": map[string]any{"type": "idle"}}}}
			}
			_ = connection.WriteJSON(map[string]any{"id": id, "result": result})
		}
	})}
	go server.Serve(listener)
	t.Cleanup(func() {
		_ = server.Close()
	})
	return home
}

func TestCodexResolveIDKeepsManagedLoadedSessionWritable(t *testing.T) {
	home := startManagedCodexFixture(t)
	t.Setenv("CODEX_HOME", home)
	codex := NewCodex("")
	codex.desktopURL = "http://127.0.0.1:1"
	session, err := codex.resolveID(context.Background(), "managed-thread")
	if err != nil || session == nil || session.Transport != codexTransportManaged {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestCodexSearchFallsBackToManagedRuntime(t *testing.T) {
	home := startManagedCodexFixture(t)
	t.Setenv("CODEX_HOME", home)
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

type scriptedCodexClient struct {
	methods []string
	params  []map[string]any
}

type sequenceCodexClient struct {
	responses []map[string]any
	index     int
}

func (c *sequenceCodexClient) Request(_ context.Context, _ string, _ map[string]any, _ time.Duration) (map[string]any, error) {
	if c.index >= len(c.responses) {
		return c.responses[len(c.responses)-1], nil
	}
	response := c.responses[c.index]
	c.index++
	return response, nil
}

func (*sequenceCodexClient) Close() error { return nil }

func managedThreadResponse(turnID, status, assistant string) map[string]any {
	return managedThreadResponseItems(turnID, status, []any{map[string]any{"type": "agentMessage", "text": assistant}})
}

func managedThreadResponseItems(turnID, status string, items []any) map[string]any {
	if len(items) == 1 {
		if item, ok := items[0].(map[string]any); ok && item["text"] == "" {
			items = nil
		}
	}
	return map[string]any{"result": map[string]any{"thread": map[string]any{
		"id": "thread", "turns": []any{map[string]any{"id": turnID, "status": status, "items": items}},
	}}}
}

func (c *scriptedCodexClient) Request(_ context.Context, method string, params map[string]any, _ time.Duration) (map[string]any, error) {
	c.methods = append(c.methods, method)
	c.params = append(c.params, params)
	if method == "thread/start" {
		return map[string]any{"result": map[string]any{
			"cwd":    "/tmp/project",
			"thread": map[string]any{"id": "thread-new", "name": "", "source": "appServer"},
		}}, nil
	}
	return map[string]any{"result": map[string]any{"turn": map[string]any{"id": "turn-new"}}}, nil
}

func (*scriptedCodexClient) Close() error { return nil }

func TestCodexStartSessionCreatesManagedThreadAndFirstTurn(t *testing.T) {
	client := &scriptedCodexClient{}
	session, sent, err := NewCodex("").startSession(context.Background(), client, surface.SessionStartOptions{
		Message:        "Build the release",
		Cwd:            "/tmp/project",
		Model:          "gpt-5.6-sol",
		ApprovalPolicy: "on-request",
	})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "thread-new" || session.Transport != codexTransportManaged || session.Status != surface.StatusBusy || session.Name != "Build the release" {
		t.Fatalf("session=%+v", session)
	}
	if sent.UUID != "turn-new" || !sent.Accepted {
		t.Fatalf("send=%+v", sent)
	}
	if strings.Join(client.methods, ",") != "thread/start,turn/start" {
		t.Fatalf("methods=%v", client.methods)
	}
	if client.params[0]["cwd"] != "/tmp/project" || client.params[0]["model"] != "gpt-5.6-sol" || client.params[0]["approvalPolicy"] != "on-request" || client.params[0]["threadSource"] != "agenthail" || client.params[0]["serviceName"] != "agenthail" {
		t.Fatalf("thread params=%v", client.params[0])
	}
	if client.params[1]["threadId"] != "thread-new" {
		t.Fatalf("turn params=%v", client.params[1])
	}
}

func TestPrepareManagedTerminalSessionUsesProviderThreadIdentity(t *testing.T) {
	client := &scriptedCodexClient{}
	session, err := prepareManagedTerminalSession(context.Background(), client, "/tmp/project", "gpt-5.6-sol")
	if err != nil {
		t.Fatal(err)
	}
	if session.ID != "thread-new" || session.Surface != surface.KindCodex || session.Transport != codexTransportManaged || session.Cwd != "/tmp/project" {
		t.Fatalf("session=%+v", session)
	}
	if strings.Join(client.methods, ",") != "thread/start" {
		t.Fatalf("methods=%v", client.methods)
	}
	if client.params[0]["threadSource"] != "agenthail-terminal" || client.params[0]["serviceName"] != "agenthail" || client.params[0]["cwd"] != "/tmp/project" || client.params[0]["model"] != "gpt-5.6-sol" {
		t.Fatalf("thread params=%v", client.params[0])
	}
}

func TestManagedStreamWaitsForNewTurnInsteadOfReplayingHistory(t *testing.T) {
	client := &sequenceCodexClient{responses: []map[string]any{
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponse("new", "inProgress", "hel"),
		managedThreadResponseItems("new", "completed", []any{
			map[string]any{"id": "interim", "type": "agentMessage", "phase": "commentary", "text": "hel"},
			map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "hello"},
		}),
	}}
	var events []surface.StreamEvent
	err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Text != "hel" || events[0].ID != "managed:new:text" || events[1].Text != "hello" || !events[1].Final || events[1].Operation != "upsert" || events[1].ID != "managed:new:text" || events[2].Kind != "done" || events[2].ID != "managed:new:done" {
		t.Fatalf("events=%+v", events)
	}
}

func TestManagedStreamCompletesAlreadyCompletedFinalWithoutPrefix(t *testing.T) {
	client := &sequenceCodexClient{responses: []map[string]any{
		managedThreadResponseItems("done", "completed", []any{
			map[string]any{"id": "interim", "type": "agentMessage", "phase": "commentary", "text": "checking"},
			map[string]any{"id": "final", "type": "agentMessage", "phase": "final_answer", "text": "finished"},
		}),
	}}
	var events []surface.StreamEvent
	err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "done", func(event surface.StreamEvent) {
		events = append(events, event)
	}, time.Second)
	if err != nil || len(events) != 2 || events[0].Text != "finished" || !events[0].Final || events[1].Kind != "done" {
		t.Fatalf("err=%v events=%+v", err, events)
	}
}

func TestManagedStreamTreatsGrowingFinalPhaseAsPartialUntilTerminal(t *testing.T) {
	client := &sequenceCodexClient{responses: []map[string]any{
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponseItems("new", "inProgress", []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "hel"}}),
		managedThreadResponseItems("new", "inProgress", []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "hello"}}),
		managedThreadResponseItems("new", "completed", []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "hello"}}),
	}}
	var events []surface.StreamEvent
	err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 2*time.Second)
	if err != nil || len(events) != 4 || events[0].Text != "hel" || events[1].Text != "hello" || events[1].Operation != "upsert" || events[2].Text != "hello" || !events[2].Final || events[3].Kind != "done" {
		t.Fatalf("err=%v events=%+v", err, events)
	}
}

func TestManagedStreamDoesNotEmitFinalForFailedTurn(t *testing.T) {
	client := &sequenceCodexClient{responses: []map[string]any{
		managedThreadResponseItems("failed", "failed", []any{map[string]any{"id": "answer", "type": "agentMessage", "phase": "final_answer", "text": "not success"}}),
	}}
	var events []surface.StreamEvent
	err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "failed", func(event surface.StreamEvent) {
		events = append(events, event)
	}, time.Second)
	if err == nil || len(events) != 0 {
		t.Fatalf("err=%v events=%+v", err, events)
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

func TestCodexManagedRuntimeStatusReportsPIDBackend(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"pid\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_DAEMON_SUPERVISOR", "")
	t.Setenv("XPC_SERVICE_NAME", "")
	status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
	if !status.Reachable || status.Durable || status.Backend != "pid" {
		t.Fatalf("status=%+v", status)
	}
	if !strings.Contains(status.Remediation, "agenthail launch codex") {
		t.Fatalf("remediation=%q", status.Remediation)
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

func TestCodexManagedRuntimeStatusReportsSupervisedPIDBackend(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"pid\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_DAEMON_SUPERVISOR", "homebrew")
	status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
	if !status.Reachable || !status.Durable || status.Backend != "pid" {
		t.Fatalf("status=%+v", status)
	}
	if status.Detail != "" || status.Remediation != "" {
		t.Fatalf("supervised status=%+v", status)
	}
}

func TestCodexManagedRuntimeStatusReportsLaunchdSupervisedPIDBackend(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"pid\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("XPC_SERVICE_NAME", "com.agenthail.daemon")
	status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
	if !status.Reachable || !status.Durable || status.Backend != "pid" {
		t.Fatalf("status=%+v", status)
	}
	if status.Detail != "" || status.Remediation != "" {
		t.Fatalf("supervised status=%+v", status)
	}
}

func TestCodexManagedRuntimeStatusRejectsUnknownSupervisor(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"pid\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	t.Setenv("AGENTHAIL_DAEMON_SUPERVISOR", "unknown")
	t.Setenv("XPC_SERVICE_NAME", "")
	status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
	if !status.Reachable || status.Durable {
		t.Fatalf("status=%+v", status)
	}
}

func TestCodexManagedRuntimeStatusReportsDurableSupervisedBackend(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\n[ \"$1 $2 $3\" = \"app-server daemon version\" ] || exit 2\nprintf '%s\\n' '{\"status\":\"running\",\"backend\":\"launchd\",\"socketPath\":\"/tmp/codex.sock\"}'\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_CODEX_BIN", script)
	status := isolatedManagedRuntime(t).RuntimeStatus(context.Background())
	if !status.Reachable || !status.Durable || status.Backend != "launchd" {
		t.Fatalf("status=%+v", status)
	}
	if status.Detail != "" {
		t.Fatalf("supervised backend should carry no degraded detail, got %q", status.Detail)
	}
	if status.Remediation != "" {
		t.Fatalf("supervised backend needs no remediation, got %q", status.Remediation)
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

func TestManagedStreamRestartsEmitIdempotentAuthoritativeSnapshots(t *testing.T) {
	var events []surface.StreamEvent
	for _, body := range []string{"hello", "hello", "hello world"} {
		client := &sequenceCodexClient{responses: []map[string]any{
			managedThreadResponse("turn-1", "inProgress", body),
			managedThreadResponse("turn-1", "inProgress", body),
		}}
		err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "", func(event surface.StreamEvent) {
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

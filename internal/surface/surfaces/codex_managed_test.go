package surfaces

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCodexTransportSeparatesDesktopManagedAndPlainCLI(t *testing.T) {
	cases := []struct {
		source           string
		status           any
		managed          bool
		desktopReachable bool
		want             string
	}{
		{"vscode", "idle", false, true, codexTransportDesktop},
		{"vscode", "notLoaded", true, true, codexTransportDesktop},
		{"vscode", "idle", true, false, codexTransportReadOnly},
		{"agenthail", "idle", true, false, codexTransportManaged},
		{"cli", "idle", true, false, codexTransportManaged},
		{"cli", "notLoaded", true, false, codexTransportReadOnly},
		{"cli", "idle", false, false, codexTransportReadOnly},
	}
	for _, test := range cases {
		if got := codexTransport(test.source, test.status, test.managed, test.desktopReachable); got != test.want {
			t.Fatalf("source=%s status=%v managed=%v desktopReachable=%v got=%s want=%s", test.source, test.status, test.managed, test.desktopReachable, got, test.want)
		}
	}
}

type fixedCodexClient struct {
	response map[string]any
}

func (c *fixedCodexClient) Request(context.Context, string, map[string]any, time.Duration) (map[string]any, error) {
	return c.response, nil
}

func (c *fixedCodexClient) Close() error { return nil }

func TestCodexHealthAcceptsManagedFallback(t *testing.T) {
	desktop := func(context.Context) (codexClient, error) { return nil, fmt.Errorf("desktop unavailable") }
	managed := func(context.Context) (codexClient, error) { return &fixedCodexClient{}, nil }
	if err := codexHealth(context.Background(), true, desktop, managed); err != nil {
		t.Fatal(err)
	}
}

func TestCodexHealthReportsBothUnavailableTransports(t *testing.T) {
	desktop := func(context.Context) (codexClient, error) { return nil, fmt.Errorf("desktop unavailable") }
	managed := func(context.Context) (codexClient, error) { return nil, fmt.Errorf("managed unavailable") }
	err := codexHealth(context.Background(), true, desktop, managed)
	if err == nil || !strings.Contains(err.Error(), "desktop unavailable") || !strings.Contains(err.Error(), "managed unavailable") {
		t.Fatalf("err=%v", err)
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
	items := []any{}
	if assistant != "" {
		items = append(items, map[string]any{"type": "agentMessage", "text": assistant})
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

func TestManagedStreamWaitsForNewTurnInsteadOfReplayingHistory(t *testing.T) {
	client := &sequenceCodexClient{responses: []map[string]any{
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponse("old", "completed", "old answer"),
		managedThreadResponse("new", "inProgress", "hel"),
		managedThreadResponse("new", "completed", "hello"),
	}}
	var events []surface.StreamEvent
	err := NewCodex("").streamManagedClient(context.Background(), client, &surface.Session{ID: "thread", Transport: codexTransportManaged}, "", func(event surface.StreamEvent) {
		events = append(events, event)
	}, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Text != "hel" || events[1].Text != "lo" || events[2].Kind != "done" {
		t.Fatalf("events=%+v", events)
	}
}

func TestCodexListPageBecomesReadOnlyWhenDesktopBridgeIsLost(t *testing.T) {
	client := &fixedCodexClient{response: map[string]any{
		"result": map[string]any{
			"data": []any{map[string]any{
				"id": "desktop-thread", "name": "Desktop thread", "source": "vscode", "status": "idle",
			}},
		},
	}}
	codex := NewCodex("")
	bridged, _, err := codex.listPage(context.Background(), client, nil, true, true)
	if err != nil || len(bridged) != 1 || bridged[0].Transport != codexTransportDesktop {
		t.Fatalf("bridged sessions=%v err=%v", bridged, err)
	}
	unbridged, _, err := codex.listPage(context.Background(), client, nil, true, false)
	if err != nil || len(unbridged) != 1 || unbridged[0].Transport != codexTransportReadOnly {
		t.Fatalf("unbridged sessions=%v err=%v", unbridged, err)
	}
	if reason := surface.ReadOnlySessionReason(&unbridged[0]); !strings.Contains(reason, "agenthail launch codex") {
		t.Fatalf("reason=%q", reason)
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
	desktop := &surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"}
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
	if reason := surface.ReadOnlySessionReason(unbridgedDesktop); reason != "Codex Desktop is not bridged; run 'agenthail launch codex'" {
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
	status := NewCodex("").RuntimeStatus(context.Background())
	if !status.Reachable || status.Durable || status.Backend != "pid" {
		t.Fatalf("status=%+v", status)
	}
	if !strings.Contains(status.Remediation, "agenthail daemon install") {
		t.Fatalf("remediation=%q", status.Remediation)
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
	status := NewCodex("").RuntimeStatus(context.Background())
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

func TestCodexEnsureRuntimeBootstrapsMissingManagedDaemonOnce(t *testing.T) {
	root := t.TempDir()
	logPath := filepath.Join(root, "calls.log")
	codeHome := filepath.Join(root, "codex-home")
	socketPath := filepath.Join(codeHome, "app-server-control", "app-server-control.sock")
	script := filepath.Join(root, "codex")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \"$AGENTHAIL_TEST_LOG\"\nif [ \"$1 $2 $3\" = \"app-server daemon bootstrap\" ]; then mkdir -p \"$(dirname \"$AGENTHAIL_TEST_SOCKET\")\"; : > \"$AGENTHAIL_TEST_SOCKET\"; exit 0; fi\nexit 2\n"
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
	if got := strings.Count(string(data), "app-server daemon bootstrap"); got != 1 {
		t.Fatalf("bootstrap calls=%d log=%q", got, data)
	}
}

func TestCodexEnsureRuntimeNamesMissingManagedDaemon(t *testing.T) {
	t.Setenv("AGENTHAIL_CODEX_BIN", filepath.Join(t.TempDir(), "missing-codex"))
	t.Setenv("CODEX_HOME", t.TempDir())
	err := NewCodex("").EnsureRuntime(context.Background())
	if err == nil || !strings.Contains(err.Error(), "managed Codex app-server") || !strings.Contains(err.Error(), "AGENTHAIL_CODEX_BIN") {
		t.Fatalf("err=%v", err)
	}
}

func TestCodexForcedRuntimeRecoveryStartsWithStaleSocket(t *testing.T) {
	root := t.TempDir()
	codeHome := filepath.Join(root, "codex-home")
	socketPath := filepath.Join(codeHome, "app-server-control", "app-server-control.sock")
	if err := os.MkdirAll(filepath.Dir(socketPath), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(socketPath, nil, 0600); err != nil {
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
	if err := NewCodex("").ensureRuntime(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(data), "app-server daemon bootstrap") {
		t.Fatalf("log=%q err=%v", data, err)
	}
}

func TestCodexBinaryPrefersManagedStandaloneRuntime(t *testing.T) {
	root := t.TempDir()
	managed := filepath.Join(root, "packages", "standalone", "current", "codex")
	if err := os.MkdirAll(filepath.Dir(managed), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(managed, []byte("#!/bin/sh\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", root)
	t.Setenv("AGENTHAIL_CODEX_BIN", "")
	path, err := codexBinary()
	if err != nil || path != managed {
		t.Fatalf("path=%q err=%v", path, err)
	}
}

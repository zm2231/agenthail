package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestDashboardListenIsLoopbackOnly(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7412", "[::1]:7412", "localhost:7412"} {
		if err := validateDashboardListen(listen); err != nil {
			t.Fatalf("%s: %v", listen, err)
		}
	}
	if err := validateDashboardListen("0.0.0.0:7412"); err == nil || !strings.Contains(err.Error(), "Tailscale Serve") {
		t.Fatalf("err=%v", err)
	}
}

func TestDashboardRequiresTokenAndRejectsCrossOriginActions(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("state status=%d", unauthorized.Code)
	}

	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/?token=secret", nil))
	if bootstrap.Code != http.StatusFound || len(bootstrap.Result().Cookies()) != 1 {
		t.Fatalf("bootstrap status=%d cookies=%d", bootstrap.Code, len(bootstrap.Result().Cookies()))
	}
	cookie := bootstrap.Result().Cookies()[0]
	if cookie.MaxAge != dashboardCookieMaxAge {
		t.Fatalf("dashboard cookie max age=%d, want %d", cookie.MaxAge, dashboardCookieMaxAge)
	}
	state := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(state, request)
	if state.Code != http.StatusOK || !strings.Contains(state.Body.String(), `"sessions"`) {
		t.Fatalf("state=%d body=%s", state.Code, state.Body.String())
	}

	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": "from", "message": "hello"})
	mutating := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	mutating.Header.Set("Origin", "https://attacker.example")
	mutating.AddCookie(cookie)
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, mutating)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin action status=%d", blocked.Code)
	}

	tailscale := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	tailscale.Header.Set("Origin", "https://agenthail.tailnet.ts.net")
	tailscale.Host = "agenthail.tailnet.ts.net"
	tailscale.AddCookie(cookie)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, tailscale)
	if allowed.Code != http.StatusOK {
		t.Fatalf("tailscale action status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestDashboardActionSendsToRegisteredSession(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": "from", "message": "ship this"})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(fake.sent) != 1 || fake.sent[0] != "ship this" {
		t.Fatalf("status=%d sent=%v body=%s", response.Code, fake.sent, response.Body.String())
	}
}

func TestDashboardCompactQueuesWorkingClaudeSession(t *testing.T) {
	_, registry, _, _, _ := daemonFixture(t)
	session := surface.Session{ID: "claude", Surface: surface.KindClaude, Name: "claude", Status: surface.StatusBusy}
	if err := registry.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	fake := &daemonSurface{
		kind:         surface.KindClaude,
		sessions:     map[string]surface.Session{session.ID: session},
		observations: map[string]*surface.TurnObservation{session.ID: {Status: surface.StatusBusy, ActiveTurnID: "turn"}},
		rejectBusy:   true,
		accepted:     true,
		caps:         surface.Capabilities{Compact: true},
	}
	d := New(registry, []surface.Surface{fake})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"compact","sessionId":"claude"}`))
	d.dashboardActionHandler(response, request)
	if response.Code != http.StatusOK || registry.QueueCount(session.ID) != 1 || !strings.Contains(response.Body.String(), `"disposition":"queued"`) {
		t.Fatalf("status=%d queue=%d body=%s", response.Code, registry.QueueCount(session.ID), response.Body.String())
	}
	rows, err := registry.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Message != "/compact" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestDashboardAliasesAndRealiasesSession(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	setAlias := func(name string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]string{"action": "alias", "sessionId": "from", "alias": name})
		request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
		request.Header.Set("Origin", "http://example.test")
		request.Host = "example.test"
		request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := setAlias("reviewer"); response.Code != http.StatusOK {
		t.Fatalf("first alias status=%d body=%s", response.Code, response.Body.String())
	}
	if response := setAlias("shipper"); response.Code != http.StatusOK {
		t.Fatalf("second alias status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := registry.LookupAlias("reviewer"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("old alias still resolves: %v", err)
	}
	alias, err := registry.ReverseAlias("from")
	if err != nil || alias != "shipper" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
	state := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	handler.ServeHTTP(state, request)
	if state.Code != http.StatusOK || !strings.Contains(state.Body.String(), `"alias":"shipper"`) {
		t.Fatalf("state=%d body=%s", state.Code, state.Body.String())
	}
	history := httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/api/history?limit=10", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	handler.ServeHTTP(history, request)
	if history.Code != http.StatusOK || !strings.Contains(history.Body.String(), `"kind":"identified"`) || !strings.Contains(history.Body.String(), `"result":"@shipper"`) {
		t.Fatalf("history=%d body=%s", history.Code, history.Body.String())
	}
}

func TestDashboardSurfaceHealthNamesDegradedCodexAndRepair(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	adapter := &healthDaemonSurface{
		daemonSurface: &daemonSurface{kind: surface.KindCodex},
		healthErr:     errors.New("Codex Desktop bridge is unavailable"),
		runtime:       surface.RuntimeStatus{Name: "Codex managed app-server", Reachable: true, Durable: true, Backend: "launchd"},
	}
	entry := d.dashboardSurfaceHealth(context.Background(), adapter, nil)
	if entry.Health != "degraded" || entry.HealthDetail != "Codex Desktop bridge is unavailable" || entry.RepairAction != "codex-launch" || entry.RepairLabel != "Launch Codex through Agenthail" {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestDashboardSurfaceHealthNamesMissingManagedRuntime(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	adapter := &healthDaemonSurface{
		daemonSurface: &daemonSurface{kind: surface.KindCodex},
		runtime:       surface.RuntimeStatus{Name: "Codex managed app-server", Detail: "socket missing"},
	}
	entry := d.dashboardSurfaceHealth(context.Background(), adapter, nil)
	if entry.Health != "degraded" || entry.HealthDetail != "socket missing" || entry.RepairAction != "runtime-ensure" || entry.RepairLabel != "Repair managed runtime" {
		t.Fatalf("entry=%+v", entry)
	}
}

func TestDashboardRepairsManagedRuntime(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	base := &daemonSurface{kind: surface.KindCodex}
	adapter := &runtimeDaemonSurface{daemonSurface: base}
	d = New(registry, []surface.Surface{adapter})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "runtime-ensure"})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || adapter.ensureCalls.Load() != 1 {
		t.Fatalf("status=%d calls=%d body=%s", response.Code, adapter.ensureCalls.Load(), response.Body.String())
	}
}

func TestDashboardExposesConversationNamingControls(t *testing.T) {
	script := string(dashboardJS)
	if !strings.Contains(script, `["/name", "Name this conversation"]`) ||
		!strings.Contains(script, `data-tool="alias"`) ||
		!strings.Contains(script, `"/name": ["alias", { alias: argument }]`) {
		t.Fatal("dashboard is missing conversation naming controls")
	}
}

func TestDashboardOverviewSubtitleUsesLiveState(t *testing.T) {
	script := string(dashboardJS)
	if strings.Contains(script, "Private and ready") ||
		!strings.Contains(script, `surface${connected === 1 ? "" : "s"} connected`) ||
		!strings.Contains(script, `message${queued === 1 ? "" : "s"} waiting`) {
		t.Fatal("overview subtitle is not derived from live state")
	}
}

func TestDashboardHidesChannelActionsUntilChannelExists(t *testing.T) {
	markup := string(dashboardHTML)
	script := string(dashboardJS)
	if !strings.Contains(markup, `id="channel-actions" hidden`) ||
		!strings.Contains(script, `$("#channel-actions").hidden = channels.length === 0`) {
		t.Fatal("channel actions are not progressively disclosed")
	}
	create := strings.Index(markup, `data-network-form="channel-create"`)
	empty := strings.Index(markup, `id="channel-list"`)
	actions := strings.Index(markup, `id="channel-actions"`)
	if create < 0 || empty <= create || actions <= empty {
		t.Fatal("channel empty state and controls are ordered incorrectly")
	}
}

func TestDashboardLongMessagesUseCleanCrop(t *testing.T) {
	styles := string(dashboardCSS)
	if strings.Contains(styles, "linear-gradient(transparent, var(--paper))") ||
		!strings.Contains(styles, "height: 1.7em") ||
		!strings.Contains(styles, "background: var(--paper)") {
		t.Fatal("long messages do not use a clean opaque crop")
	}
}

func TestDashboardConversationEntryAlignsLatestTurn(t *testing.T) {
	script := string(dashboardJS)
	if !strings.Contains(script, "app.pendingEntryScroll = true") ||
		!strings.Contains(script, `latest.getBoundingClientRect().top - chatBody.getBoundingClientRect().top`) ||
		!strings.Contains(script, "alignTranscriptTop(chatBody)") ||
		!strings.Contains(script, `querySelectorAll("tr, pre, blockquote, li, h2, h3, h4, p")`) {
		t.Fatal("conversation entry does not align to the latest turn boundary")
	}
}

func TestDashboardExposesSurfaceHealthAndRepairs(t *testing.T) {
	script := string(dashboardJS)
	page := string(dashboardHTML)
	for _, fragment := range []string{
		`id="surface-health-list"`,
		`data-surface-repair=`,
		`await action(repair.dataset.surfaceRepair)`,
		`Claude Code needs to reconnect to your signed-in account.`,
	} {
		if !strings.Contains(page+script, fragment) {
			t.Fatalf("dashboard is missing %q", fragment)
		}
	}
}

func TestDashboardClipsLongMessagesWithoutFadingText(t *testing.T) {
	styles := string(dashboardCSS)
	if !strings.Contains(styles, ".turn-crop:after {") || !strings.Contains(styles, "background: var(--paper);") || strings.Contains(styles, "linear-gradient") {
		t.Fatal("long-message crop must use an opaque cutoff without a text fade")
	}
	if !strings.Contains(string(dashboardJS), "Show full message") {
		t.Fatal("long-message crop has no expansion control")
	}
}

func TestDashboardListsModelsForNewConversation(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.modelOptions = []surface.ModelOption{{ID: "gpt-5.6-sol", DisplayName: "GPT-5.6 Sol", Default: true}}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/models?surface=codex", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"id":"gpt-5.6-sol"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestDashboardStartsWritableCodexConversation(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	cwd := t.TempDir()
	body, _ := json.Marshal(map[string]string{
		"action": "session-create", "surface": "codex", "message": "Build this", "cwd": cwd,
		"model": "gpt-5.6-sol", "approvalPolicy": "on-request", "alias": "builder",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(fake.startOptions) != 1 || fake.startOptions[0].Cwd != cwd || fake.startOptions[0].Model != "gpt-5.6-sol" || fake.startOptions[0].ApprovalPolicy != "on-request" {
		t.Fatalf("options=%+v", fake.startOptions)
	}
	session, err := registry.Session("started")
	if err != nil || session.Transport != "managed" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	if alias, err := registry.ReverseAlias("started"); err != nil || alias != "builder" {
		t.Fatalf("alias=%q err=%v", alias, err)
	}
	history, err := registry.ListHistory(5, "started")
	if err != nil || len(history) != 1 || history[0].Kind != "sent" || history[0].Result != "started-turn" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestDashboardReturnsCreatedSessionWhenInitialDeliveryIsUnknown(t *testing.T) {
	d, registry, fake, _, _ := daemonFixture(t)
	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn outcome unknown"))
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "session-create", "surface": "codex", "message": "Build this", "cwd": t.TempDir()})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusAccepted || !strings.Contains(response.Body.String(), `"unknown":true`) || !strings.Contains(response.Body.String(), `"id":"started"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := registry.Session("started"); err != nil {
		t.Fatalf("created session was not registered: %v", err)
	}
	history, err := registry.ListHistory(5, "started")
	if err != nil || len(history) != 1 || history[0].Kind != "unknown" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestDashboardStartCwdExpandsHomeAndRejectsFiles(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	project := filepath.Join(home, "project")
	if err := os.Mkdir(project, 0700); err != nil {
		t.Fatal(err)
	}
	resolved, err := dashboardStartCwd("~/project")
	if err != nil || resolved != project {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	file := filepath.Join(home, "file")
	if err := os.WriteFile(file, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := dashboardStartCwd(file); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("err=%v", err)
	}
}

func TestDashboardCreatesAndRegistersNotionThread(t *testing.T) {
	registry, err := registrypkg.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { registry.Close() })
	const threadID = "3978aba0-0606-80ac-a1ae-00a9eb229fc0"
	fake := &daemonSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true, turnID: threadID}
	handler := New(registry, []surface.Surface{fake}).dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "notion-create", "message": "Research the launch", "alias": "research", "model": "fast"})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), threadID) || len(fake.sent) != 1 || fake.sent[0] != "Research the launch" || len(fake.models) != 1 || fake.models[0] != "fast" {
		t.Fatalf("status=%d sent=%v models=%v body=%s", response.Code, fake.sent, fake.models, response.Body.String())
	}
	session, err := registry.Session(threadID)
	if err != nil || session.Name != "research" || session.Surface != surface.KindNotion {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	resolved, err := registry.ResolveTarget("research")
	if err != nil || resolved != threadID {
		t.Fatalf("resolved=%q err=%v", resolved, err)
	}
	history, err := registry.ListHistory(5, threadID)
	if err != nil || len(history) != 1 || history[0].SessionID != threadID || history[0].Kind != "sent" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestDashboardStreamsSelectedSessionOverSSE(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	fake.caps = surface.Capabilities{Stream: true}
	fake.streamEvents = []surface.StreamEvent{{Kind: "text", Text: "hello"}, {Kind: "tool_use", Text: "tests"}, {Kind: "done"}}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/stream?id=from", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") || !strings.Contains(body, `"kind":"text","text":"hello"`) || !strings.Contains(body, `"kind":"done"`) {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), body)
	}
}

func TestDashboardShutdownCancelsActiveStreams(t *testing.T) {
	serverCtx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dashboard := &dashboardServer{cancel: cancel}
	dashboard.server = &http.Server{
		BaseContext: func(net.Listener) context.Context { return serverCtx },
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			close(started)
			w.Header().Set("Content-Type", "text/event-stream")
			w.WriteHeader(http.StatusOK)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}),
	}
	go dashboard.server.Serve(listener)
	requestDone := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if response != nil {
			response.Body.Close()
		}
		requestDone <- requestErr
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("stream did not start")
	}
	if err := dashboard.shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("stream request survived shutdown")
	}
}

func TestDashboardUsesOneSelectedSessionEventStream(t *testing.T) {
	source := string(dashboardJS)
	for _, fragment := range []string{
		"new EventSource(`/api/stream?id=${encodeURIComponent(session.id)}`)",
		`app.liveSource && app.liveSessionID === session.id`,
		`app.liveText += delta.text || ""`,
		`setInterval(() =>`,
		`}, 30000)`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("dashboard live stream missing %q", fragment)
		}
	}
}

func TestDashboardCapabilitiesMakeUnloadedCodexReadOnly(t *testing.T) {
	capabilities, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded")}, surface.Capabilities{Send: true, Model: true})
	if !readOnly || reason == "" || capabilities.Send || capabilities.Model {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", capabilities, readOnly, reason)
	}
}

func TestDashboardCapabilitiesKeepUnloadedDesktopThreadWritable(t *testing.T) {
	capabilities := surface.Capabilities{Send: true, Model: true}
	got, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"}, capabilities)
	if readOnly || reason != "" || !got.Send || !got.Model {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", got, readOnly, reason)
	}
}

func TestDashboardCapabilitiesDisableUnbridgedDesktopThread(t *testing.T) {
	capabilities := surface.Capabilities{Send: true, Model: true}
	got, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "vscode", Transport: "readOnly"}, capabilities)
	if !readOnly || got.Send || got.Model || !strings.Contains(reason, "agenthail launch codex") {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", got, readOnly, reason)
	}
}

func TestDashboardCapabilitiesMakePlainCodexTerminalReadOnly(t *testing.T) {
	capabilities, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"}, surface.Capabilities{Send: true, Steer: true, Compact: true})
	if !readOnly || reason == "" || capabilities.Send || capabilities.Steer || capabilities.Compact {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", capabilities, readOnly, reason)
	}
}

func TestDashboardRemoteQRCodeRequiresExplicitReveal(t *testing.T) {
	source := string(dashboardJS)
	if !strings.Contains(source, "remoteQRVisible: false") || !strings.Contains(source, "data-reveal-remote-qr") {
		t.Fatal("remote QR does not default to an explicit reveal state")
	}
	hidden := strings.Index(source, "const remoteQR = app.remoteQRVisible")
	if hidden < 0 {
		t.Fatal("remote QR conditional is missing")
	}
	image := strings.Index(source[hidden:], `/api/settings/remote-qr`)
	reveal := strings.Index(source[hidden:], "data-reveal-remote-qr")
	if image < 0 || reveal < 0 || image > reveal {
		t.Fatal("remote QR image is not confined to the revealed branch")
	}
	if !strings.Contains(source, "navigator.clipboard.writeText(app.settings.remoteAccess.url)") {
		t.Fatal("hidden QR removed copy-phone-link behavior")
	}
}

func TestDashboardNormalizesEmptyStateAndShowsDeliveryOutcomes(t *testing.T) {
	source := string(dashboardJS)
	for _, fragment := range []string{
		"surfaces: state.surfaces || []",
		"sessions: state.sessions || []",
		"queue: state.queue || []",
		"delivery-history",
		`["sent", "delivered", "failed", "unknown", "expired", "canceled"]`,
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("dashboard source missing %q", fragment)
		}
	}
	if !strings.Contains(string(dashboardHTML), `id="delivery-history"`) {
		t.Fatal("delivery outcomes have no rendered surface")
	}
}

func TestDashboardPreservesTranscriptStateAcrossPolls(t *testing.T) {
	source := string(dashboardJS)
	for _, fragment := range []string{
		"expandedTurns: new Set()",
		"app.transcriptSignature === signature",
		"app.expandedTurns.add(turn.dataset.turnKey)",
		"chatBody.scrollHeight - chatBody.scrollTop - chatBody.clientHeight <= 24",
		"Math.min(previousScrollTop, chatBody.scrollHeight - chatBody.clientHeight)",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("dashboard source missing %q", fragment)
		}
	}
}

func TestDashboardComposerDistinguishesStopQueueAndSteer(t *testing.T) {
	source := string(dashboardJS)
	for _, fragment := range []string{
		`drafts: new Map()`,
		`app.drafts.set(app.selected.id, $("#message").value)`,
		`$("#message").value = app.drafts.get(session.id) || ""`,
		`sendButton.dataset.mode = stopping ? "interrupt" : "send"`,
		`"Send queues this for next. Steer now changes the turn in progress."`,
		`steerButton.hidden = !(busy && capabilities.steer && hasMessage && !readOnly)`,
		`await action("interrupt")`,
		`await action("steer", { message })`,
		`"Compact queued and will run when this turn finishes."`,
		"queued until this turn finishes.",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("dashboard source missing %q", fragment)
		}
	}
	if !strings.Contains(string(dashboardHTML), `id="steer-message"`) {
		t.Fatal("busy composer has no inline steer action")
	}
}

func TestDashboardExposesNewConversationFormForCodexAndNotion(t *testing.T) {
	for _, fragment := range []string{
		`id="new-conversation-form"`,
		`<option value="on-request">Ask when needed</option>`,
		`fetch("/api/models?surface=codex")`,
		`values.surface === "notion" ? "notion-create" : "session-create"`,
		`response.sessionId || response.session?.id`,
	} {
		if !strings.Contains(string(dashboardHTML)+string(dashboardJS), fragment) {
			t.Fatalf("new conversation surface missing %q", fragment)
		}
	}
}

func TestDashboardExplainsSurfacePresenceWindow(t *testing.T) {
	source := string(dashboardJS)
	for _, fragment := range []string{
		"<span>Current</span>",
		`? "Open now"`,
		"`Past ${app.state.codexRecentHours || 5}h`",
	} {
		if !strings.Contains(source, fragment) {
			t.Fatalf("dashboard source missing %q", fragment)
		}
	}
}

func TestDashboardOnlyShowsProvenAttentionItems(t *testing.T) {
	d, registry, _, _, target := daemonFixture(t)
	if err := registry.QueueMessage(target.ID, "requires a decision"); err != nil {
		t.Fatal(err)
	}
	state, err := d.dashboardState(context.Background())
	if err != nil || len(state.Attention) != 0 {
		t.Fatalf("pending attention=%+v err=%v", state.Attention, err)
	}
	item, err := registry.ClaimNextMessage(target.ID, time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := registry.DeadLetterUnknown(item.ID, errors.New("connection closed")); err != nil {
		t.Fatal(err)
	}
	state, err = d.dashboardState(context.Background())
	if err != nil || len(state.Attention) != 1 || state.Attention[0].QueueID != item.ID || state.Attention[0].RequestedAction == "" {
		t.Fatalf("dead attention=%+v err=%v", state.Attention, err)
	}
	if err := registry.CancelMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	state, err = d.dashboardState(context.Background())
	if err != nil || len(state.Attention) != 0 {
		t.Fatalf("resolved attention=%+v err=%v", state.Attention, err)
	}
	for _, fragment := range []string{"attention: state.attention || []", "attentionPanel.hidden = attention.length === 0", `id="attention-panel" hidden`} {
		if !strings.Contains(string(dashboardJS)+string(dashboardHTML), fragment) {
			t.Fatalf("dashboard attention surface missing %q", fragment)
		}
	}
}

func TestDashboardRejectsReadOnlyCodexRoutingDestination(t *testing.T) {
	daemon, registry, _, _, target := daemonFixture(t)
	target.Source = "cli"
	target.Transport = "readOnly"
	if err := registry.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CreateChannel("reviewers"); err != nil {
		t.Fatal(err)
	}
	queueID, err := registry.QueueMessageWithOptions(target.ID, "do not retry", "", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ClaimNextMessage(target.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registry.NackMessage(queueID, fmt.Errorf("read only"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"action":"channel-add","channel":"reviewers","targetId":"to"}`,
		`{"action":"relay-add","fromId":"from","toId":"to","pattern":".*"}`,
		fmt.Sprintf(`{"action":"queue-retry","queueId":%d}`, queueID),
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(body))
		daemon.dashboardActionHandler(response, request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "read only") {
			t.Fatalf("body=%s code=%d", response.Body.String(), response.Code)
		}
	}
	item, err := registry.QueueItem(queueID)
	if err != nil || item.Status != "dead" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	members, err := registry.ChannelMembers("reviewers")
	if err != nil || len(members) != 0 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	routes, err := registry.ListRoutes()
	if err != nil || len(routes) != 0 {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
	if err := registry.AddToChannel("reviewers", target.ID); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"channel-send","channel":"reviewers","message":"handoff"}`))
	daemon.dashboardActionHandler(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "1 failed") {
		t.Fatalf("body=%s code=%d", response.Body.String(), response.Code)
	}
}

func TestDashboardStateCachesSurfaceDiscovery(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	dashboard := &dashboardServer{token: "secret"}
	handler := d.dashboardHandler(dashboard)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		req.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("state request %d status=%d body=%s", i, res.Code, res.Body.String())
		}
	}
	if got := fake.listCalls.Load(); got != 1 {
		t.Fatalf("surface list called %d times, want one cached discovery", got)
	}
}

func TestDashboardHistoryIsAuthorizedAndPaginated(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	for i := 0; i < 4; i++ {
		if err := registry.RecordHistory(registrypkg.HistoryEntry{Kind: "sent", Message: fmt.Sprintf("message %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/history?limit=2", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/history?limit=2", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var first struct {
		Items      []dashboardHistory `json:"items"`
		HasMore    bool               `json:"hasMore"`
		NextBefore int64              `json:"nextBefore"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextBefore != first.Items[1].ID {
		t.Fatalf("first page=%+v", first)
	}
	nextRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/history?limit=2&before=%d", first.NextBefore), nil)
	nextRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, nextRequest)
	if nextResponse.Code != http.StatusOK || strings.Contains(nextResponse.Body.String(), fmt.Sprintf(`"id":%d`, first.Items[1].ID)) {
		t.Fatalf("second page status=%d body=%s", nextResponse.Code, nextResponse.Body.String())
	}
	filteredRequest := httptest.NewRequest(http.MethodGet, "/api/history?limit=25&q=message+3&kind=sent", nil)
	filteredRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	filteredResponse := httptest.NewRecorder()
	handler.ServeHTTP(filteredResponse, filteredRequest)
	if filteredResponse.Code != http.StatusOK || !strings.Contains(filteredResponse.Body.String(), "message 3") || strings.Contains(filteredResponse.Body.String(), "message 2") {
		t.Fatalf("filtered status=%d body=%s", filteredResponse.Code, filteredResponse.Body.String())
	}
}

func TestDashboardStatePrefersLiveSurfaceStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, registry, fake, from, _ := daemonFixture(t)
	if err := registry.SaveRuntimeState(from.ID, surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	from.Status = surface.StatusBusy
	from.LastActive = time.Now()
	fake.sessions[from.ID] = from

	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range state.Sessions {
		if session.ID == from.ID {
			if session.Status != surface.StatusBusy {
				t.Fatalf("dashboard status=%s, want live status %s", session.Status, surface.StatusBusy)
			}
			return
		}
	}
	t.Fatal("live session missing from dashboard state")
}

func TestDashboardSessionPresenceUsesSurfaceSpecificTruth(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		session     surface.Session
		queueCount  int
		open        bool
		wantCurrent bool
		wantReason  string
	}{
		{name: "open idle Claude", session: surface.Session{Surface: surface.KindClaude, Status: surface.StatusIdle}, open: true, wantCurrent: true, wantReason: "open"},
		{name: "closed busy Claude is stale", session: surface.Session{Surface: surface.KindClaude, Status: surface.StatusBusy}, wantCurrent: false},
		{name: "queued closed Claude", session: surface.Session{Surface: surface.KindClaude}, queueCount: 1, wantCurrent: true, wantReason: "queued"},
		{name: "recent Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: now.Add(-4 * time.Hour)}, wantCurrent: true, wantReason: "recent"},
		{name: "old Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: now.Add(-6 * time.Hour)}, wantCurrent: false},
		{name: "historical Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), LastActive: now.Add(-time.Hour)}, wantCurrent: false},
		{name: "busy Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusBusy, LastActive: now.Add(-48 * time.Hour)}, wantCurrent: true, wantReason: "working"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, reason := dashboardSessionPresence(test.session, test.queueCount, test.open, 5, now)
			if current != test.wantCurrent || reason != test.wantReason {
				t.Fatalf("current=%v reason=%q, want current=%v reason=%q", current, reason, test.wantCurrent, test.wantReason)
			}
		})
	}
}

func TestClaudeProcessOpenRejectsOtherProcesses(t *testing.T) {
	if claudeProcessOpen(context.Background(), os.Getpid()) {
		t.Fatal("non-Claude process was recognized as an open Claude session")
	}
	if claudeProcessOpen(context.Background(), 0) {
		t.Fatal("zero PID was recognized as alive")
	}
}

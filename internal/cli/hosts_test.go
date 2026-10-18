package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const forgeHostID = "h0123456789ab"

type fakeDaemonAPI struct {
	mu       sync.Mutex
	matches  map[string][]remoteTarget
	actions  []map[string]any
	keys     []string
	response string
}

func (f *fakeDaemonAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/api/v1/resolve":
		if r.URL.Query().Get("scope") != "remote" {
			http.Error(w, `{"error":{"code":"bad","message":"remote scope expected"}}`, http.StatusBadRequest)
			return
		}
		matches := f.matches[r.URL.Query().Get("target")]
		if matches == nil {
			matches = []remoteTarget{}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"matches": matches})
	case "/api/v1/actions":
		body, _ := io.ReadAll(r.Body)
		var action map[string]any
		_ = json.Unmarshal(body, &action)
		f.actions = append(f.actions, action)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		_, _ = io.WriteString(w, f.response)
	default:
		http.NotFound(w, r)
	}
}

func (f *fakeDaemonAPI) recorded() ([]map[string]any, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]map[string]any{}, f.actions...), append([]string{}, f.keys...)
}

var forgeBuilder = remoteTarget{ID: hosts.Qualify(forgeHostID, "remote-1"), Surface: surface.KindCodex, Name: "remote work", Alias: "builder", Host: hostRef{ID: forgeHostID, Name: "forge", State: "connected"}}

func federatedCLIFixture(t *testing.T, daemonRunning bool) (*App, *registry.Registry, *cliSurface, *fakeDaemonAPI) {
	t.Helper()
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{}, caps: surface.Capabilities{Send: true, Steer: true, Interrupt: true, Compact: true, Stream: true, Reply: true}}
	app, reg := cliFixture(t, fake)
	if _, err := reg.AddHost(registry.PairedHost{ID: forgeHostID, Name: "forge", Endpoint: "https://forge.example.ts.net", Address: "100.64.0.2", Token: "ahd_synthetic"}); err != nil {
		t.Fatal(err)
	}
	projection := `{"id":"` + forgeBuilder.ID + `","surface":"codex","name":"remote work","alias":"builder","host":{"id":"` + forgeHostID + `","name":"forge","state":"unreachable"}}`
	if _, _, err := reg.ApplyHostCatalogRow(forgeHostID, registry.HostCatalogRow{Session: surface.Session{ID: "remote-1", Surface: surface.KindCodex, Name: "remote work", Cwd: "/home/remote/src"}, EntityID: forgeBuilder.ID, Projection: json.RawMessage(projection), UnavailableReason: "host forge is unreachable"}); err != nil {
		t.Fatal(err)
	}
	api := &fakeDaemonAPI{matches: map[string][]remoteTarget{"@builder": {forgeBuilder}, "builder": {forgeBuilder}}, response: `{"ok":true,"result":{"evidence":"delivered","status":"sent","sessionId":"` + forgeBuilder.ID + `","turnId":"turn-7"}}`}
	server := httptest.NewServer(api)
	t.Cleanup(server.Close)
	app.catalogDaemonRunning = func() bool { return daemonRunning }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: server.URL, token: "dashboard-synthetic", httpClient: server.Client()}, nil
	}
	return app, reg, fake, api
}

func TestHostQualifiedSendRoutesThroughDaemon(t *testing.T) {
	app, _, fake, api := federatedCLIFixture(t, true)
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "forge::@builder", "hello forge"}) })
	if err != nil || !strings.Contains(output, "sent to forge::@builder (turn turn-7)") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, keys := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "send" || actions[0]["sessionId"] != forgeBuilder.ID || actions[0]["message"] != "hello forge" {
		t.Fatalf("actions=%v", actions)
	}
	if _, hasSource := actions[0]["sourceSessionId"]; hasSource {
		t.Fatalf("remote send carried a local source session: %v", actions[0])
	}
	if !strings.HasPrefix(keys[0], "cli-") {
		t.Fatalf("idempotency key=%q", keys[0])
	}
	if len(fake.sent) != 0 {
		t.Fatalf("local surface received %v", fake.sent)
	}
}

func TestTargetOnTwoHostsIsAmbiguous(t *testing.T) {
	app, reg, fake, api := federatedCLIFixture(t, true)
	fake.sessions["local-1"] = surface.Session{ID: "local-1", Surface: surface.KindCodex, Name: "local work"}
	if err := reg.RegisterSession(fake.sessions["local-1"]); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetAlias("builder", "local-1"); err != nil {
		t.Fatal(err)
	}
	identity, err := reg.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	err = app.Run([]string{"send", "@builder", "which one"})
	if err == nil || !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "forge::@builder") || !strings.Contains(err.Error(), identity.Name+"::@builder") {
		t.Fatalf("err=%v", err)
	}
	if actions, _ := api.recorded(); len(actions) != 0 || len(fake.sent) != 0 {
		t.Fatalf("ambiguous target delivered: actions=%v local=%v", actions, fake.sent)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", identity.Name + "::@builder", "the local one"}) })
	if err != nil || len(fake.sent) != 1 {
		t.Fatalf("local-qualified send output=%q err=%v sent=%v", output, err, fake.sent)
	}
}

func TestRemoteOnlyTargetSteersAndInterruptsThroughDaemon(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	if output, err := captureStdout(t, func() error { return app.Run([]string{"steer", "builder", "turn left"}) }); err != nil || !strings.Contains(output, "Sent to forge::@builder.") {
		t.Fatalf("steer output=%q err=%v", output, err)
	}
	if err := app.Run([]string{"interrupt", "forge::builder"}); err != nil {
		t.Fatal(err)
	}
	actions, _ := api.recorded()
	if len(actions) != 2 || actions[0]["action"] != "steer" || actions[0]["message"] != "turn left" || actions[1]["action"] != "interrupt" || actions[1]["sessionId"] != forgeBuilder.ID {
		t.Fatalf("actions=%v", actions)
	}
}

func TestHeldRemoteSendReportsQueuedNotSent(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true,"result":{"evidence":"queued","status":"queued","sessionId":"` + forgeBuilder.ID + `","queueId":3,"detail":"host forge is unreachable; queued until it reconnects"}}`
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "forge::@builder", "later"}) })
	if err != nil || !strings.Contains(output, "queued for forge::@builder: host forge is unreachable") || strings.Contains(output, "sent to") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	api.response = `{"ok":true,"status":"submitted"}`
	output, err = captureStdout(t, func() error { return app.Run([]string{"send", "forge::@builder", "pending"}) })
	if err != nil || !strings.Contains(output, "delivery submitted to forge::@builder") || strings.Contains(output, "sent to") {
		t.Fatalf("pending replay output=%q err=%v", output, err)
	}
	if err := app.Run([]string{"send", "forge::@builder", "now", "--no-queue"}); err == nil || !strings.Contains(err.Error(), "--no-queue") {
		t.Fatalf("--no-queue err=%v", err)
	}
}

func TestLocalOnlyCommandRefusesRemoteSession(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	err := app.Run([]string{"compact", "forge::@builder"})
	if err == nil || !strings.Contains(err.Error(), "forge::@builder is a session on host forge") {
		t.Fatalf("err=%v", err)
	}
	if err := app.Run([]string{"send", "nowhere::x", "hi"}); err == nil || !strings.Contains(err.Error(), `no paired host is named "nowhere"`) {
		t.Fatalf("unknown host err=%v", err)
	}
	if actions, _ := api.recorded(); len(actions) != 0 {
		t.Fatalf("actions=%v", actions)
	}
}

func TestStoppedDaemonStillRefusesAmbiguityFromMirror(t *testing.T) {
	app, reg, fake, _ := federatedCLIFixture(t, false)
	fake.sessions["local-1"] = surface.Session{ID: "local-1", Surface: surface.KindCodex, Name: "local work"}
	if err := reg.RegisterSession(fake.sessions["local-1"]); err != nil {
		t.Fatal(err)
	}
	if err := reg.SetAlias("builder", "local-1"); err != nil {
		t.Fatal(err)
	}
	if err := app.Run([]string{"send", "@builder", "which one", "--no-queue"}); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("err=%v", err)
	}
	if err := app.Run([]string{"send", "forge::builder", "hi"}); err == nil || !strings.Contains(err.Error(), "daemon, which is not running") {
		t.Fatalf("err=%v", err)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("sent=%v", fake.sent)
	}
}

func TestThreadCreateOnHostKeepsRemoteCwd(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	api.response = `{"ok":true,"session":{"id":"` + hosts.Qualify(forgeHostID, "new-1") + `","surface":"codex","cwd":"/home/remote/src/x"},"result":{"uuid":"turn-9","accepted":true}}`
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "start there", "--host", "forge", "--cwd", "~/src/x"})
	})
	if err != nil || !strings.Contains(output, "created forge::codex/new-1 in /home/remote/src/x and started turn turn-9") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "session-create" || actions[0]["host"] != "forge" || actions[0]["cwd"] != "~/src/x" || actions[0]["surface"] != "codex" {
		t.Fatalf("actions=%v", actions)
	}
}

func TestListShowsRemoteRowsWithHostAndUnavailableState(t *testing.T) {
	app, _, _, _ := federatedCLIFixture(t, true)
	output, err := captureStdout(t, func() error { return app.Run([]string{"list"}) })
	if err != nil || !strings.Contains(output, "HOST") || !strings.Contains(output, "forge") || !strings.Contains(output, "down") || !strings.Contains(output, "@builder") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"list", "--json"}) })
	if err != nil {
		t.Fatal(err)
	}
	var document struct {
		Sessions []listSessionView `json:"sessions"`
	}
	if err := json.Unmarshal([]byte(output), &document); err != nil {
		t.Fatalf("decode %q: %v", output, err)
	}
	if len(document.Sessions) != 1 || document.Sessions[0].ID != forgeBuilder.ID || document.Sessions[0].Host == nil || document.Sessions[0].Host.Name != "forge" || document.Sessions[0].UnavailableReason == "" {
		t.Fatalf("sessions=%+v", document.Sessions)
	}
}

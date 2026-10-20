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
	mu        sync.Mutex
	matches   map[string][]remoteTarget
	actions   []map[string]any
	keys      []string
	response  string
	responses map[string]string
	gets      []string
	snapshot  string
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
		if response, found := f.responses[action["action"].(string)]; found {
			_, _ = io.WriteString(w, response)
			return
		}
		_, _ = io.WriteString(w, f.response)
	case "/api/v1/snapshot":
		_, _ = io.WriteString(w, f.snapshot)
	case "/api/v1/session-metadata":
		f.gets = append(f.gets, r.URL.RequestURI())
		_, _ = io.WriteString(w, `{"sessionId":"`+r.URL.Query().Get("id")+`","model":"gpt-remote","goal":{"objective":"ship the remote picker","status":"active"}}`)
	case "/api/v1/history":
		f.gets = append(f.gets, r.URL.RequestURI())
		_, _ = io.WriteString(w, `{"items":[{"id":4,"createdAt":"2026-10-19T10:00:00Z","kind":"delivered","target":"@builder","message":"synthetic history line","evidence":"delivered"}],"hasMore":false}`)
	case "/api/v1/hosts":
		body, _ := io.ReadAll(r.Body)
		var request map[string]any
		_ = json.Unmarshal(body, &request)
		request["method"] = r.Method
		f.actions = append(f.actions, request)
		_, _ = io.WriteString(w, `{"host":{"id":"`+forgeHostID+`","name":"forge","address":"100.64.0.2","state":"connecting"}}`)
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
	if _, _, err := reg.ApplyHostCatalogRow(forgeHostID, registry.HostCatalogRow{Session: surface.Session{ID: "remote-1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "remote work", Cwd: "/home/remote/src"}, EntityID: forgeBuilder.ID, Projection: json.RawMessage(projection), UnavailableReason: "host forge is unreachable"}); err != nil {
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
	fake.sessions["local-1"] = surface.Session{ID: "local-1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "local work"}
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

func TestChannelsAndRelaysReachARemoteSession(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	if _, err := app.Registry.CreateChannel("ops"); err != nil {
		t.Fatal(err)
	}
	if output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "add", "ops", "forge::@builder"}) }); err != nil || !strings.Contains(output, "forge::@builder") {
		t.Fatalf("channel add output=%q err=%v", output, err)
	}
	members, err := app.Registry.ChannelMembers("ops")
	if err != nil || len(members) != 1 || members[0] != "remote-1" {
		t.Fatalf("members=%v err=%v", members, err)
	}
	t.Setenv("CLAUDE_CODE_SESSION_ID", "")
	if output, err := captureStdout(t, func() error { return app.Run([]string{"channel", "send", "ops", "status?"}) }); err != nil || !strings.Contains(output, "[QUEUE] forge::@builder (held for its host)") {
		t.Fatalf("channel send output=%q err=%v", output, err)
	}
	if held, err := app.Registry.HostDeliveries(forgeHostID); err != nil || len(held) != 1 || held[0].SessionID != "remote-1" {
		t.Fatalf("held=%+v err=%v", held, err)
	}
	if output, err := captureStdout(t, func() error { return app.Run([]string{"relay", "add", "forge::@builder", "forge::@builder", "done"}) }); err != nil || !strings.Contains(output, "relay on host forge") {
		t.Fatalf("relay add output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["action"] != "relay-add" || actions[0]["fromId"] != forgeBuilder.ID || actions[0]["pattern"] != "done" {
		t.Fatalf("actions=%v", actions)
	}
	if err := app.Run([]string{"send", "nowhere::x", "hi"}); err == nil || !strings.Contains(err.Error(), `no paired host is named "nowhere"`) {
		t.Fatalf("unknown host err=%v", err)
	}
}

func TestRemoteSessionCommandsRouteThroughDaemon(t *testing.T) {
	app, _, fake, api := federatedCLIFixture(t, true)
	api.responses = map[string]string{
		"compact":                  `{"ok":true,"result":{"evidence":"delivered","status":"sent"}}`,
		"model":                    `{"ok":true,"result":{"sessionId":"remote-builder","action":"model","evidence":"delivered","source":"codex","output":"gpt-next"}}`,
		"session-fork":             `{"ok":true,"result":{"session":{"id":"` + hosts.Qualify(forgeHostID, "fork-1") + `","surface":"codex"}}}`,
		"native-queue":             `{"ok":true,"result":{"items":[]}}`,
		"session-lifecycle-status": `{"ok":true,"result":{"state":"running"}}`,
		"alias":                    `{"ok":true,"alias":"builder2"}`,
		"goal-set":                 `{"ok":true,"result":null}`,
		"goal-budget":              `{"ok":true,"result":null}`,
	}
	run := func(args ...string) string {
		t.Helper()
		output, err := captureStdout(t, func() error { return app.Run(args) })
		if err != nil {
			t.Fatalf("%v: err=%v", args, err)
		}
		return output
	}
	if output := run("compact", "forge::@builder"); !strings.Contains(output, "compacted forge::@builder") {
		t.Fatalf("compact output=%q", output)
	}
	if output := run("model", "forge::@builder"); strings.TrimSpace(output) != "gpt-remote" {
		t.Fatalf("model read output=%q", output)
	}
	if output := run("model", "forge::@builder", "gpt-next"); !strings.Contains(output, "model delivered") || !strings.HasSuffix(strings.TrimSpace(output), "gpt-next") {
		t.Fatalf("model set output=%q", output)
	}
	if output := run("goal", "forge::@builder"); !strings.Contains(output, "ship the remote picker") {
		t.Fatalf("goal read output=%q", output)
	}
	run("goal", "forge::@builder", "set", "finish", "it")
	run("goal", "forge::@builder", "budget", "clear")
	if output := run("identify", "forge::@builder", "builder2"); !strings.Contains(output, "forge::@builder2") {
		t.Fatalf("identify output=%q", output)
	}
	run("thread", "fork", "forge::@builder", "--model", "gpt-next", "--alias", "branch")
	run("thread", "queue", "forge::@builder", "list")
	if output := run("thread", "status", "forge::@builder"); !strings.Contains(output, "running") {
		t.Fatalf("status output=%q", output)
	}
	if output := run("queue", "forge::@builder", "when", "idle"); !strings.Contains(output, "sent to forge::@builder") {
		t.Fatalf("queue output=%q", output)
	}
	if output := run("history", "forge::@builder", "150"); !strings.Contains(output, "forge::@builder synthetic history line") {
		t.Fatalf("history output=%q", output)
	}
	run("thread", "stop", "forge::@builder")
	run("thread", "resume", "forge::@builder")
	run("thread", "logs", "forge::@builder")
	actions, _ := api.recorded()
	want := []string{"compact", "model", "goal-set", "goal-budget", "alias", "session-fork", "native-queue", "session-lifecycle-status", "send", "session-lifecycle-stop", "session-lifecycle-resume", "session-lifecycle-logs"}
	if len(actions) != len(want) {
		t.Fatalf("actions=%v", actions)
	}
	for index, name := range want {
		if actions[index]["action"] != name {
			t.Fatalf("action %d=%v want %s", index, actions[index], name)
		}
	}
	for index, action := range actions {
		if action["sessionId"] != forgeBuilder.ID {
			t.Fatalf("action %d sessionId=%v", index, action["sessionId"])
		}
	}
	if actions[1]["model"] != "gpt-next" || actions[2]["message"] != "finish it" || actions[3]["message"] != "" || actions[4]["alias"] != "builder2" || actions[5]["alias"] != "branch" {
		t.Fatalf("payloads=%v", actions)
	}
	if fork, _ := actions[5]["fork"].(map[string]any); fork["model"] != "gpt-next" {
		t.Fatalf("fork payload=%v", actions[5])
	}
	if queue, _ := actions[6]["nativeQueue"].(map[string]any); queue["queueAction"] != "list" {
		t.Fatalf("native queue payload=%v", actions[6])
	}
	if actions[8]["busyDelivery"] != "queue" || actions[8]["message"] != "when idle" {
		t.Fatalf("queue payload=%v", actions[8])
	}
	if len(api.gets) != 3 || !strings.Contains(api.gets[2], "/api/v1/history?") || !strings.Contains(api.gets[2], "limit=150") {
		t.Fatalf("gets=%v", api.gets)
	}
	if len(fake.sent) != 0 {
		t.Fatalf("local surface received %v", fake.sent)
	}
}

func TestHeldSendForRemoteSessionIsLocalQueueData(t *testing.T) {
	app, reg, _, api := federatedCLIFixture(t, true)
	id, err := reg.QueueHostDelivery("remote-1", "held for forge", "cli-synthetic", []byte(`{"action":"send"}`), false)
	if err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"queue", "list", "--target", "forge::@builder"}) })
	if err != nil || !strings.Contains(output, "held for forge") {
		t.Fatalf("list output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"queue", "clear", "forge::@builder"}) })
	if err != nil || !strings.Contains(output, "canceled 1 queued message(s)") {
		t.Fatalf("clear output=%q err=%v id=%d", output, err, id)
	}
	if actions, _ := api.recorded(); len(actions) != 0 {
		t.Fatalf("actions=%v", actions)
	}
}

func TestStoppedDaemonStillRefusesAmbiguityFromMirror(t *testing.T) {
	app, reg, fake, _ := federatedCLIFixture(t, false)
	fake.sessions["local-1"] = surface.Session{ID: "local-1", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "local work"}
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
	output, err := captureStdout(t, func() error { return app.Run([]string{"list", "--all"}) })
	if err != nil || !strings.Contains(output, "HOST") || !strings.Contains(output, "forge") || !strings.Contains(output, "down") || !strings.Contains(output, "@builder") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"list", "--all", "--json"}) })
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

func TestHostsAddPassesAddressAndName(t *testing.T) {
	app, _, _, api := federatedCLIFixture(t, true)
	link := "agenthail://pair?endpoint=https%3A%2F%2Fforge.example.ts.net%3A7498&secret=synthetic"
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"hosts", "add", link, "--address", "100.64.0.2", "--name", "forge"})
	})
	if err != nil || !strings.Contains(output, "paired forge ("+forgeHostID+") at 100.64.0.2") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	actions, _ := api.recorded()
	if len(actions) != 1 || actions[0]["method"] != http.MethodPost || actions[0]["pairingUrl"] != link || actions[0]["address"] != "100.64.0.2" || actions[0]["name"] != "forge" {
		t.Fatalf("requests=%v", actions)
	}
}

func TestHostFlagsStayScopedToTheirCommands(t *testing.T) {
	for _, flag := range []string{"--address", "--name", "--host"} {
		err := (&App{}).Run([]string{"dashboard", "status", flag, "100.64.0.2"})
		if err == nil || !strings.Contains(err.Error(), "unknown flag") {
			t.Fatalf("dashboard %s err=%v", flag, err)
		}
	}
}

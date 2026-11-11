package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface"
)

func allowAgentScope(daemon.ScopeRequest) (*daemon.ScopeReply, error) {
	return &daemon.ScopeReply{}, nil
}

type scopeServer struct {
	mu       sync.Mutex
	asked    []daemon.ScopeRequest
	refusal  string
	accepted string
}

func (s *scopeServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != daemon.ScopePath {
		http.NotFound(w, r)
		return
	}
	var request daemon.ScopeRequest
	_ = json.NewDecoder(r.Body).Decode(&request)
	s.mu.Lock()
	s.asked = append(s.asked, request)
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if s.refusal != "" {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(s.refusal))
		return
	}
	_, _ = w.Write([]byte(s.accepted))
}

func (s *scopeServer) requests() []daemon.ScopeRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]daemon.ScopeRequest(nil), s.asked...)
}

func scopedFixture(t *testing.T, server *scopeServer) (*App, *cliSurface) {
	t.Helper()
	target := surface.Session{ID: "target", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusBusy}
	worker := surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Transport: "app-server", Status: surface.StatusIdle}
	fake := &cliSurface{kind: surface.KindCodex, sessions: map[string]surface.Session{"target": target, "worker": worker}, caps: surface.Capabilities{Send: true, Steer: true, Interrupt: true}}
	app, r := cliFixture(t, fake)
	for _, session := range []surface.Session{target, worker} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	daemonAt(t, app, server)
	return app, fake
}

func TestAnAgentsWriteOutsideItsReachIsRefusedWithTheRuleBeforeAnythingIsSent(t *testing.T) {
	server := &scopeServer{refusal: `{"scope":true,"error":{"code":"out_of_scope","message":"That session is in your project but is not one you spawned or, as its project's coordinator, lead; you may send it a queued message, nothing more."},"retryable":false,"detail":{"rule":"not_spawned_or_led"}}`}
	app, fake := scopedFixture(t, server)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	for _, command := range [][]string{{"steer", "codex:target", "change course"}, {"interrupt", "codex:target"}, {"send", "codex:target", "hello"}} {
		_, err := captureStdout(t, func() error { return app.Run(command) })
		if err == nil || !strings.Contains(err.Error(), "refused (out_of_scope, rule not_spawned_or_led): That session is in your project but is not one you spawned") {
			t.Fatalf("%s: err=%v", command[0], err)
		}
	}
	if len(fake.sent) != 0 || len(fake.steered) != 0 {
		t.Fatalf("a refused write reached the session: sent=%q steered=%q", fake.sent, fake.steered)
	}
	asked := server.requests()
	if len(asked) != 3 || asked[0].SessionID != "worker" || asked[0].Action != "control" || asked[0].Target != "target" || asked[2].Action != "send" || asked[2].Mode != "queue" {
		t.Fatalf("scope requests=%+v", asked)
	}
}

func TestEveryAgentWriteAsksBeforeItActs(t *testing.T) {
	server := &scopeServer{refusal: `{"scope":true,"error":{"code":"out_of_scope","message":"no"},"detail":{"rule":"different_project"}}`}
	app, fake := scopedFixture(t, server)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	cases := []struct {
		command []string
		action  string
		mode    string
	}{
		{[]string{"send", "codex:target", "hello", "--no-queue"}, "send", "now"},
		{[]string{"thread", "clean", "codex:target"}, "control", ""},
		{[]string{"thread", "adopt", "codex:target"}, "control", ""},
		{[]string{"project", "rename", "widget", "gadget"}, "operator", ""},
		{[]string{"ledger", "work", "add", "a task", "--project", "repo:github.com/example/other"}, "ledger", ""},
		{[]string{"queue", "codex:target", "hello"}, "send", "queue"},
	}
	for _, c := range cases {
		before := len(server.requests())
		if _, err := captureStdout(t, func() error { return app.Run(c.command) }); err == nil || !strings.Contains(err.Error(), "refused (out_of_scope") {
			t.Fatalf("%v: err=%v", c.command, err)
		}
		asked := server.requests()
		if len(asked) != before+1 || asked[before].Action != c.action || asked[before].Mode != c.mode {
			t.Fatalf("%v: scope requests=%+v", c.command, asked[before:])
		}
	}
	if asked := server.requests(); asked[4].Project != "repo:github.com/example/other" {
		t.Fatalf("the ledger write asked for %q", asked[4].Project)
	}
	if len(fake.sent) != 0 || len(fake.steered) != 0 {
		t.Fatalf("a refused write reached the session: sent=%q", fake.sent)
	}
}

func TestAnAgentsWriteInsideItsReachGoesThrough(t *testing.T) {
	server := &scopeServer{accepted: `{"targetId":"target"}`}
	app, fake := scopedFixture(t, server)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "hello"}) }); err != nil || len(fake.sent) != 1 {
		t.Fatalf("err=%v sent=%q", err, fake.sent)
	}
	if asked := server.requests(); len(asked) != 1 || asked[0].SessionID != "worker" || asked[0].Target != "target" {
		t.Fatalf("scope requests=%+v", asked)
	}
}

func TestThePersonsTerminalKeepsItsReachEvenWithFrom(t *testing.T) {
	server := &scopeServer{refusal: `{"scope":true,"error":{"code":"out_of_scope","message":"no"}}`}
	app, fake := scopedFixture(t, server)
	if _, err := captureStdout(t, func() error { return app.Run([]string{"send", "codex:target", "hello", "--from", "worker"}) }); err != nil || len(fake.sent) != 1 {
		t.Fatalf("err=%v sent=%q", err, fake.sent)
	}
	if asked := server.requests(); len(asked) != 0 {
		t.Fatalf("the person's terminal asked for scope: %+v", asked)
	}
}

func TestAnAgentsWriteRefusesWhileTheDaemonIsStopped(t *testing.T) {
	app, fake := scopedFixture(t, &scopeServer{accepted: `{}`})
	app.catalogDaemonRunning = func() bool { return false }
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	_, err := captureStdout(t, func() error { return app.Run([]string{"steer", "codex:target", "change course"}) })
	if err == nil || !strings.Contains(err.Error(), "agenthail daemon start") || len(fake.steered) != 0 {
		t.Fatalf("err=%v steered=%q", err, fake.steered)
	}
}

func TestAThreadAnAgentStartsTakesItsCeiling(t *testing.T) {
	cwd := mustEvalSymlinks(t, t.TempDir())
	starter := &starterCLISurface{
		cliSurface: &cliSurface{kind: surface.KindCodex},
		session:    &surface.Session{ID: "thread-child", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: cwd, Status: surface.StatusBusy, Source: "agenthail"},
		delivery:   &surface.SendResult{UUID: "turn-1", Accepted: true},
		ready:      true,
	}
	app := threadFixture(t, starter)
	if err := app.Registry.RegisterSession(surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Cwd: cwd}); err != nil {
		t.Fatal(err)
	}
	server := &scopeServer{accepted: `{"ok":true,"session":{"id":"thread-child","surface":"codex","cwd":"` + cwd + `"},"result":{"uuid":"turn-1","accepted":true}}`}
	daemonAt(t, app, server)
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd, "--alias", "helper", "--approval", "never"})
	})
	if err != nil || !strings.Contains(output, "created @helper in "+cwd+" and started turn turn-1") {
		t.Fatalf("output=%q err=%v", output, err)
	}
	asked := server.requests()
	var create map[string]any
	if len(asked) != 1 || asked[0].Action != "create" || asked[0].SessionID != "worker" || json.Unmarshal(asked[0].Create, &create) != nil {
		t.Fatalf("scope requests=%+v", asked)
	}
	if create["surface"] != "codex" || create["cwd"] != cwd || create["approvalPolicy"] != "never" || create["alias"] != "helper" || create["host"] != nil {
		t.Fatalf("create request=%v", create)
	}
	if len(starter.options) != 0 {
		t.Fatalf("an agent's start ran in the CLI: %+v", starter.options)
	}

	for _, failed := range []string{
		`{"ok":false,"status":"failed","code":"create_failed","error":"delivery did not start"}`,
		`{"error":{"code":"create_failed","message":"delivery did not start"}}`,
	} {
		server.refusal = failed
		if _, err := captureStdout(t, func() error {
			return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd})
		}); err == nil || strings.Contains(err.Error(), "refused") || !strings.Contains(err.Error(), "delivery did not start") {
			t.Fatalf("a failed start %s: %v", failed, err)
		}
	}
	server.refusal = `{"scope":true,"error":{"code":"exceeds_ceiling","message":"That folder is outside the folders you may work in."},"retryable":false,"detail":{"axis":"roots"}}`
	if _, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", cwd})
	}); err == nil || !strings.Contains(err.Error(), "refused (exceeds_ceiling): That folder is outside") {
		t.Fatalf("err=%v", err)
	}
}

func daemonAt(t *testing.T, app *App, server *scopeServer) {
	t.Helper()
	app.scopeOverride = nil
	http := httptest.NewServer(server)
	t.Cleanup(http.Close)
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) {
		return &daemonAPIClient{baseURL: http.URL, token: "dashboard-synthetic", httpClient: http.Client()}, nil
	}
}

func TestAnAgentStartsSessionsOnlyOnItsOwnHost(t *testing.T) {
	app := threadFixture(t, &starterCLISurface{cliSurface: &cliSurface{kind: surface.KindCodex}})
	if err := app.Registry.RegisterSession(surface.Session{ID: "worker", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_SESSION_ID", "worker")
	_, err := captureStdout(t, func() error {
		return app.Run([]string{"thread", "create", "codex", "Build it", "--cwd", "/srv/work", "--host", "elsewhere"})
	})
	if err == nil || !strings.Contains(err.Error(), "refused (out_of_scope)") {
		t.Fatalf("err=%v", err)
	}
}

package cli

import (
	"context"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehooks"
	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/testkit"
)

func serveTestDaemon(t *testing.T, store *registry.Registry, adapters []surface.Surface, listen string) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := daemon.New(store, adapters).Serve(ctx); err != nil {
			t.Errorf("serve: %v", err)
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if conn, err := net.Dial("tcp", listen); err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon's dashboard never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return func() { cancel(); <-done }
}

// The caller sets HOME first: the dashboard config is written there.
func startTestDaemon(t *testing.T, app *App, store *registry.Registry, claudeHome string) {
	t.Helper()
	serveAppDaemon(t, app, store, []surface.Surface{surfaces.NewClaude(nil, claudeHome)})
}

func serveCreates(t *testing.T, app *App) func() {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	return serveAppDaemon(t, app, app.registry(), app.allSurfaces())
}

func serveAppDaemon(t *testing.T, app *App, store *registry.Registry, adapters []surface.Surface) func() {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	listen := listener.Addr().String()
	listener.Close()
	if err := daemon.SaveDashboardConfig(daemon.DashboardConfig{Enabled: true, Listen: listen}); err != nil {
		t.Fatal(err)
	}
	stopServing := serveTestDaemon(t, store, adapters, listen)
	var once sync.Once
	stop := func() {
		once.Do(func() {
			stopServing()
			app.catalogDaemonRunning = func() bool { return false }
			app.daemonAPIOverride = nil
		})
	}
	t.Cleanup(stop)
	client, err := newDaemonAPIClient()
	if err != nil {
		t.Fatal(err)
	}
	app.catalogDaemonRunning = func() bool { return true }
	app.daemonAPIOverride = func() (*daemonAPIClient, error) { return client, nil }
	return stop
}

func TestTheCLIReadsAndAnswersATerminalQuestionTheDaemonHolds(t *testing.T) {
	h, stop := newPromptRestart(t)
	t.Cleanup(stop)
	h.heldRequest()
	if _, err := claudehooks.InstallAt(filepath.Join(h.claude.Root, "settings.json"), "/usr/local/bin/agenthail"); err != nil {
		t.Fatal(err)
	}
	claude := surfaces.NewClaude(nil, h.claude.Home)
	app := &App{Registry: h.store, Surfaces: []SurfaceEntry{{Name: string(surface.KindClaude), Surface: claude}}, DefaultTimeout: claudeFixtureTimeout, daemonService: &fakeService{},
		catalogDaemonRunning: func() bool { return true }, daemonAPIOverride: func() (*daemonAPIClient, error) { return h.client, nil }}
	session, err := h.store.Session(restartConversation)
	if err != nil {
		t.Fatal(err)
	}
	if !surface.EffectiveCapabilities(claude, session).Respond {
		t.Fatal("the CLI's own adapter does not offer answers, so this test would not reach the case where it holds none")
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"requests", restartConversation}) })
	if err != nil || !strings.Contains(output, "Which branch?") {
		t.Fatalf("requests = %q, %v", output, err)
	}
	output, err = captureStdout(t, func() error { return app.Run([]string{"answer", restartConversation, "2"}) })
	if err != nil || !strings.Contains(output, "answered") {
		t.Fatalf("answer = %q, %v", output, err)
	}
	decision := h.decision()
	if decision == nil || decision.Behavior != "allow" {
		t.Fatalf("decision = %+v", decision)
	}
	if answers, _ := decision.UpdatedInput["answers"].(map[string]any); answers["Which branch?"] != "next" {
		t.Fatalf("updatedInput = %+v", decision.UpdatedInput)
	}
}

type daemonOnlyAnswers struct {
	*surfaces.Claude
	t *testing.T
}

func (s daemonOnlyAnswers) Requests(context.Context, *surface.Session) (surface.RequestState, error) {
	s.t.Error("the CLI read requests in-process while a daemon ran")
	return surface.RequestState{}, nil
}

func (s daemonOnlyAnswers) Respond(context.Context, *surface.Session, string, surface.Decision) (string, error) {
	s.t.Error("the CLI answered in-process while a daemon ran")
	return "", nil
}

func TestTheCLIAnswersAHostSessionsQuestionThroughTheDaemon(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app, r, host, client, id := hostedClaudeApp(t)
	startTestDaemon(t, app, r, host.Home)
	app.Surfaces[0].Surface = daemonOnlyAnswers{Claude: app.Surfaces[0].Surface.(*surfaces.Claude), t: t}
	if _, err := client.Send(context.Background(), claudehost.SendRequest{SessionID: id, UUID: uuid.NewString(), Text: "QUESTION", Origin: &claudehost.Origin{Kind: "human"}}); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the question", 15*time.Second, func() bool {
		output, err := captureStdout(t, func() error { return app.Run([]string{"requests", "@worker"}) })
		if err != nil {
			t.Fatal(err)
		}
		return strings.Contains(output, "Which color?")
	})
	output, err := captureStdout(t, func() error { return app.Run([]string{"answer", "@worker", "2"}) })
	if err != nil || !strings.Contains(output, "answered") {
		t.Fatalf("answer = %q, %v", output, err)
	}
	testkit.Eventually(t, "the answered turn", 15*time.Second, func() bool {
		turns := host.Turns(t, id)
		return len(turns) == 1 && turns[0].Reply == `question:allow:{"Which color?":"Blue"}`
	})
	if err := app.Run([]string{"answer", "@worker", "2"}); err == nil || !strings.Contains(err.Error(), "no open requests") {
		t.Fatalf("second answer = %v, want no open requests", err)
	}
	testkit.Eventually(t, "the answer in history", 5*time.Second, func() bool {
		entries, err := r.ListHistory(50, id)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.Kind == "request-answered" && entry.Result == "answered by the CLI" {
				return true
			}
		}
		return false
	})
}

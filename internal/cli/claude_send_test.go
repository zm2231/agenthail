package cli

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const (
	terminalConversation = "a4027b2e-9e6f-4f49-ba51-df0b6ec70e02"
	exitedPID            = 2147483647
)

type claudePosts struct {
	mu     sync.Mutex
	bodies []string
}

func (p *claudePosts) request(_ context.Context, method, url string, _ map[string]string, body, _ string, _ string, _ time.Duration) (int, string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if method == "POST" && strings.Contains(url, "/v1/code/sessions/") {
		p.bodies = append(p.bodies, body)
	}
	return 200, `{}`, nil
}

func (p *claudePosts) count(message string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	count := 0
	for _, body := range p.bodies {
		if strings.Contains(body, `"content":"`+message+`"`) {
			count++
		}
	}
	return count
}

// exitedClaude is an exited Claude conversation registered in the CLI, with a
// running Claude host that resumes it and a peer manager that takes the
// agent messages the CLI sends.
type exitedClaude struct {
	app     *App
	r       *registry.Registry
	fixture *claudetest.Fixture
	host    *claudehosttest.Fixture
	peers   *claudetest.PeerManager
	session surface.Session
}

func exitedClaudeApp(t *testing.T) exitedClaude {
	t.Helper()
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaude("", fixture.Home)
	exited := surface.Session{ID: terminalConversation, Bridge: "session_terminal", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "fixture terminal", PID: exitedPID, Status: surface.StatusBusy, Cwd: fixture.Home, Transcript: fixture.Transcript(terminalConversation, fixture.Home)}
	if err := r.RegisterSession(exited); err != nil {
		t.Fatal(err)
	}
	return exitedClaude{app: app, r: r, fixture: fixture, host: host, peers: claudetest.ListenPeerManager(t, fixture.Home), session: exited}
}

func TestSendResumesExitedTerminalSessionInTheHostAndTheAliasStaysOnTheConversation(t *testing.T) {
	e := exitedClaudeApp(t)
	if err := e.r.SetAlias("worker", e.session.ID); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return e.app.Run([]string{"send", "@worker", "hello"}) })
	if err != nil || !strings.HasPrefix(output, "accepted by Claude socket") || e.peers.Count("hello") != 1 {
		t.Fatalf("output=%q err=%v peer=%d", output, err, e.peers.Count("hello"))
	}
	if sid, err := e.r.LookupAlias("worker"); err != nil || sid != terminalConversation {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	spawns := e.host.Spawns(t, terminalConversation)
	if resumed, err := e.r.Session(e.session.ID); err != nil || len(spawns) != 1 || resumed.RuntimeKind != surface.RuntimeClaudeHost || resumed.PID != spawns[0].PID || resumed.Bridge != "" {
		t.Fatalf("resumed=%+v spawns=%+v err=%v", resumed, spawns, err)
	}
	for _, target := range []string{"@worker", terminalConversation, "claude:" + terminalConversation} {
		if _, err := captureStdout(t, func() error { return e.app.Run([]string{"send", target, "again"}) }); err != nil {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
	if resumes := len(e.host.Spawns(t, terminalConversation)); resumes != 1 || e.peers.Count("again") != 3 {
		t.Fatalf("resumes=%d again=%d", resumes, e.peers.Count("again"))
	}
}

func TestSendToExitedSessionByExactRegisteredID(t *testing.T) {
	for _, target := range []string{terminalConversation, "claude:" + terminalConversation} {
		t.Run(target, func(t *testing.T) {
			e := exitedClaudeApp(t)
			if _, err := captureStdout(t, func() error { return e.app.Run([]string{"send", target, "hello"}) }); err != nil || e.peers.Count("hello") != 1 {
				t.Fatalf("err=%v peer=%d", err, e.peers.Count("hello"))
			}
		})
	}
}

func TestFragmentDoesNotResolveAnExitedHistoricalSession(t *testing.T) {
	e := exitedClaudeApp(t)
	for _, target := range []string{"fixture terminal", "session_term", "claude:fixture terminal", "claude:session_term"} {
		if err := e.app.Run([]string{"send", target, "hello"}); err == nil {
			t.Fatalf("fragment %q resolved an exited historical session", target)
		}
	}
	if len(e.host.Spawns(t, terminalConversation)) != 0 || e.peers.Count("hello") != 0 {
		t.Fatal("a fragment resumed or delivered to an exited historical session")
	}
}

func TestResolvingARegisteredSessionTheSurfaceCannotGiveDoesNotRefreshIt(t *testing.T) {
	for name, resolveErr := range map[string]error{"not listed": surface.ErrSessionNotFound, "surface unavailable": surface.DeliveryUnavailable(errors.New("fixture surface is down"))} {
		t.Run(name, func(t *testing.T) {
			app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude, resolveErr: resolveErr})
			if err := r.RegisterSession(surface.Session{ID: "session_closed", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle}); err != nil {
				t.Fatal(err)
			}
			time.Sleep(1100 * time.Millisecond)
			cutoff := time.Now()
			for _, target := range []string{"session_closed", "claude:session_closed"} {
				if _, _, err := app.resolveTarget(context.Background(), target); err != nil {
					t.Fatalf("target=%s err=%v", target, err)
				}
			}
			if before, err := r.SessionUpdatedBefore("session_closed", cutoff); err != nil || !before {
				t.Fatalf("resolving refreshed the registered session: before=%v err=%v", before, err)
			}
		})
	}
}

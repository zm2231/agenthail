package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
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

func (p *claudePosts) request(_ context.Context, method, url string, _ map[string]string, body string, _ surfaces.CookieSource, _ time.Duration) (int, string, error) {
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
	app.Surfaces[0].Surface = surfaces.NewClaude(nil, fixture.Home)
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

// exitedHostSession creates a host session named host-replace through the CLI,
// as `thread create --name` does, and ends its Claude process.
func exitedHostSession(t *testing.T) (*coordinatorCLI, string) {
	t.Helper()
	c := newCoordinatorCLI(t)
	output, err := c.run(t, "thread", "create", "claude", "start the work", "--name", "host-replace", "--cwd", c.host.Cwd, "--json")
	if err != nil {
		t.Fatalf("output=%q err=%v", output, err)
	}
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal([]byte(output), &created); err != nil || created.Session.ID == "" {
		t.Fatalf("output=%q err=%v", output, err)
	}
	c.firstTurn(t, created.Session.ID)
	end(t, c, created.Session.ID)
	return c, created.Session.ID
}

// end kills a session's Claude process and waits until the host has let it go.
func end(t *testing.T, c *coordinatorCLI, conversation string) {
	t.Helper()
	spawns := c.host.Spawns(t, conversation)
	pid := spawns[len(spawns)-1].PID
	_ = syscall.Kill(pid, syscall.SIGKILL)
	claudehosttest.WaitFor(t, "the Claude process to end", func() bool { return !claudehosttest.Alive(pid) })
	claudehosttest.WaitFor(t, "the host to let the session go", func() bool {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		listing, err := claudehost.NewClient(c.claude.Home).List(ctx)
		if err != nil {
			return false
		}
		for _, session := range listing.Sessions {
			if session.SessionID == conversation {
				return false
			}
		}
		return true
	})
}

func TestSendToAnExitedHostSessionResumesItDeliversAndKeepsItsName(t *testing.T) {
	c, conversation := exitedHostSession(t)
	peers := claudetest.ListenPeerManager(t, c.claude.Home)
	if err := c.r.SetAlias("replacer", conversation); err != nil {
		t.Fatal(err)
	}
	for i, target := range []string{conversation, "claude:" + conversation, "@replacer"} {
		if i > 0 {
			end(t, c, conversation)
		}
		message := fmt.Sprintf("message %d", i)
		if output, err := c.run(t, "send", target, message); err != nil {
			t.Fatalf("target=%s output=%q err=%v", target, output, err)
		}
		if delivered := peers.Count(message); delivered != 1 {
			t.Fatalf("target=%s delivered=%d", target, delivered)
		}
		if spawns := c.host.Spawns(t, conversation); len(spawns) != i+2 {
			t.Fatalf("target=%s spawns=%d, want a resume per send", target, len(spawns))
		}
		registered, err := c.r.Session(conversation)
		if err != nil || registered.Name != "host-replace" {
			t.Fatalf("target=%s the resumed session lost its name: %+v err=%v spawns=%+v", target, registered, err, c.host.Spawns(t, conversation))
		}
	}
}

func TestSendByTheNameOfAnExitedSessionGivesItsIDAndAlias(t *testing.T) {
	c, conversation := exitedHostSession(t)
	for _, alias := range []string{"", "replacer"} {
		if alias != "" {
			if err := c.r.SetAlias(alias, conversation); err != nil {
				t.Fatal(err)
			}
		}
		for _, target := range []string{"host-replace", "claude:host-replace"} {
			_, err := c.run(t, "send", target, "by name")
			want := conversation
			if alias != "" {
				want += " (@" + alias + ")"
			}
			if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "a name does not resume") {
				t.Fatalf("alias=%q target=%s err=%v", alias, target, err)
			}
		}
	}
	if spawns := c.host.Spawns(t, conversation); len(spawns) != 1 {
		t.Fatalf("a name resumed the session: spawns=%d", len(spawns))
	}
}

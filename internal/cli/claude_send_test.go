package cli

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

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

func exitedClaudeApp(t *testing.T) (*App, *registry.Registry, *claudetest.Fixture, *claudePosts, surface.Session) {
	t.Helper()
	fixture := claudetest.New(t)
	posts := &claudePosts{}
	app, r := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	app.Surfaces[0].Surface = surfaces.NewClaudeWithRequest("", fixture.Home, posts.request)
	exited := surface.Session{ID: "session_terminal", Surface: surface.KindClaude, Name: "fixture terminal", PID: exitedPID, Status: surface.StatusBusy, Cwd: fixture.Home, Transcript: fixture.Transcript(terminalConversation, fixture.Home)}
	if err := r.RegisterSession(exited); err != nil {
		t.Fatal(err)
	}
	return app, r, fixture, posts, exited
}

func TestSendResumesExitedTerminalSessionAndTheAliasFollowsIt(t *testing.T) {
	app, r, fixture, posts, exited := exitedClaudeApp(t)
	fixture.ResumeBridge("session_resumed")
	if err := r.SetAlias("worker", exited.ID); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"send", "@worker", "hello"}) })
	if err != nil || !strings.HasPrefix(output, "sent (turn ") || posts.count("hello") != 1 {
		t.Fatalf("output=%q err=%v posts=%d", output, err, posts.count("hello"))
	}
	if sid, err := r.LookupAlias("worker"); err != nil || sid != "session_resumed" {
		t.Fatalf("alias=%q err=%v", sid, err)
	}
	if _, err := r.Session(exited.ID); err == nil {
		t.Fatal("the exited row was not merged into the resumed session")
	}
	for _, target := range []string{"@worker", terminalConversation, "claude:" + terminalConversation} {
		if _, err := captureStdout(t, func() error { return app.Run([]string{"send", target, "again"}) }); err != nil {
			t.Fatalf("target=%s err=%v", target, err)
		}
	}
	if resumes := fixture.Invocations("--bg", "--resume", terminalConversation); resumes != 1 || posts.count("again") != 3 {
		t.Fatalf("resumes=%d again=%d", resumes, posts.count("again"))
	}
}

func TestSendToExitedSessionByExactRegisteredID(t *testing.T) {
	for _, target := range []string{"session_terminal", "claude:session_terminal"} {
		t.Run(target, func(t *testing.T) {
			app, _, fixture, posts, _ := exitedClaudeApp(t)
			fixture.ResumeBridge("session_terminal")
			if _, err := captureStdout(t, func() error { return app.Run([]string{"send", target, "hello"}) }); err != nil || posts.count("hello") != 1 {
				t.Fatalf("err=%v posts=%d", err, posts.count("hello"))
			}
		})
	}
}

func TestFragmentDoesNotResolveAnExitedHistoricalSession(t *testing.T) {
	app, _, fixture, posts, _ := exitedClaudeApp(t)
	for _, target := range []string{"fixture terminal", "session_term"} {
		if err := app.Run([]string{"send", target, "hello"}); err == nil {
			t.Fatalf("fragment %q resolved an exited historical session", target)
		}
	}
	if fixture.Invocations("--bg", "--resume", terminalConversation) != 0 || posts.count("hello") != 0 {
		t.Fatal("a fragment resumed or delivered to an exited historical session")
	}
}

func TestSendWithReplyToSessionResumedUnderNewIDDoesNotWaitOnTheOldOne(t *testing.T) {
	app, _, fixture, posts, _ := exitedClaudeApp(t)
	fixture.ResumeBridge("session_resumed")
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"send", "session_terminal", "hello", "--reply", "--timeout", "2s"})
	})
	if err != nil || !strings.HasPrefix(output, "sent (turn ") || posts.count("hello") != 1 {
		t.Fatalf("output=%q err=%v posts=%d", output, err, posts.count("hello"))
	}
}

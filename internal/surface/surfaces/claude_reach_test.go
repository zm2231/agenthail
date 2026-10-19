package surfaces

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const exitedPID = 2147483647

type claudePosts struct {
	mu     sync.Mutex
	bodies []string
}

func (p *claudePosts) request(_ context.Context, method, url string, _ map[string]string, body string, _ CookieSource, _ time.Duration) (int, string, error) {
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

func exitedSession(fixture *claudetest.Fixture) surface.Session {
	return surface.Session{ID: fixtureConversation, Bridge: "session_fixture", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: exitedPID, Status: surface.StatusBusy, Cwd: fixture.Home, Transcript: fixture.Transcript(fixtureConversation, fixture.Home)}
}

// hostResume is an exited conversation and a running Claude host that can
// resume it.
func hostResume(t *testing.T) (*claudetest.Fixture, *claudehosttest.Fixture, *Claude) {
	t.Helper()
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	return fixture, host, NewClaude(nil, fixture.Home)
}

func TestClaudeConcurrentTurnsToAnExitedSessionResumeItOnceInTheHost(t *testing.T) {
	fixture, host, c := hostResume(t)
	var wait sync.WaitGroup
	errs := make([]error, 2)
	ids := []string{uuid.NewString(), uuid.NewString()}
	for index, message := range []string{"first", "second"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session := exitedSession(fixture)
			_, err := c.SendHumanTurn(surface.WithMessageUUID(context.Background(), ids[index]), &session, message, nil)
			if err == nil && (session.RuntimeKind != surface.RuntimeClaudeHost || session.ID != fixtureConversation || session.Bridge != "") {
				t.Errorf("session=%+v", session)
			}
			errs[index] = err
		}()
	}
	wait.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errs=%v", errs)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 || !slices.Contains(spawns[0].Argv, "--resume") && !slices.Contains(spawns[0].Argv, "--resume="+fixtureConversation) {
		t.Fatalf("spawns=%+v, want one resume", spawns)
	}
	claudehosttest.WaitFor(t, "both turns", func() bool { return len(host.Turns(t, fixtureConversation)) == 2 })
	for _, turn := range host.Turns(t, fixtureConversation) {
		if !slices.Contains(ids, turn.UUID) || string(turn.Origin) != `{"kind":"human"}` {
			t.Fatalf("turn=%+v, want a person's turn under its intent uuid", turn)
		}
	}
}

func TestClaudeSendHoldsWhileResumedJobIsStartingWithoutRecord(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "starting", 0)
	session := exitedSession(fixture)
	observation, err := c.Observe(context.Background(), &session)
	if err != nil || observation.Status != surface.StatusOffline {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if _, err := c.Send(context.Background(), &session, "hello"); !surface.IsDeliveryHeld(err) {
		t.Fatalf("err=%v", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("resumed while the job was starting: %+v", spawns)
	}
}

func TestClaudeSendHoldsForLiveProcessWithoutMessaging(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Record(fixtureConversation, fixture.Home, "")
	session := exitedSession(fixture)
	if _, err := c.Send(context.Background(), &session, "hello"); !surface.IsDeliveryHeld(err) {
		t.Fatalf("err=%v", err)
	}
	if fixture.Invocations("agents", "--json", "--all") != 0 || len(host.Spawns(t, fixtureConversation)) != 0 {
		t.Fatal("a held conversation was resumed")
	}
}

func TestClaudeObserveExitedSessionIsAtRestOnlyWithoutLiveProcess(t *testing.T) {
	fixture := claudetest.New(t)
	c := NewClaude(nil, fixture.Home)
	session := exitedSession(fixture)
	observation, err := c.Observe(context.Background(), &session)
	if err != nil || observation.Status != surface.StatusIdle || observation.ActiveTurnID != "" || observation.CompletedTurnID != "fixture-message" {
		t.Fatalf("at rest observation=%+v err=%v", observation, err)
	}
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", fixture.PID)
	observation, err = c.Observe(context.Background(), &session)
	if err != nil || observation.Status != surface.StatusOffline {
		t.Fatalf("held observation=%+v err=%v", observation, err)
	}
}

func TestClaudeResumeThatClaudeRefusesSaysWhy(t *testing.T) {
	fixture, host, c := hostResume(t)
	host.Refuse(t, fixtureConversation, "Error: Settings file not found: /fixture/settings.json")
	session := exitedSession(fixture)
	_, err := c.Send(context.Background(), &session, "hello")
	if err == nil || surface.IsDeliveryOutcomeUnknown(err) || surface.IsDeliveryHeld(err) || !strings.Contains(err.Error(), "Claude refused to resume "+fixtureConversation) || !strings.Contains(err.Error(), "Settings file not found") {
		t.Fatalf("err=%v", err)
	}
}

func TestClaudeResumeWithoutTheHostIsHeld(t *testing.T) {
	fixture := claudetest.New(t)
	session := exitedSession(fixture)
	_, err := NewClaude(nil, fixture.Home).Send(context.Background(), &session, "hello")
	if !surface.IsDeliveryHeld(err) {
		t.Fatalf("err=%v, want held until the host runs", err)
	}
}

func TestClaudeSendResumesTerminalSessionInItsTranscriptCwd(t *testing.T) {
	fixture, host, c := hostResume(t)
	session := exitedSession(fixture)
	session.Cwd = ""
	result, err := c.SendHumanTurn(context.Background(), &session, "hello", nil)
	if err != nil || !result.Accepted || session.ID != fixtureConversation || session.RuntimeKind != surface.RuntimeClaudeHost || session.Transport != "uds" {
		t.Fatalf("result=%+v session=%+v err=%v", result, session, err)
	}
	spawns := host.Spawns(t, fixtureConversation)
	if len(spawns) != 1 || !samePath(t, spawns[0].Cwd, fixture.Home) {
		t.Fatalf("spawns=%+v, want one resume in %s", spawns, fixture.Home)
	}
	claudehosttest.WaitFor(t, "the turn", func() bool {
		turns := host.Turns(t, fixtureConversation)
		return len(turns) == 1 && turns[0].Text == "hello" && turns[0].UUID == result.UUID
	})
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestClaudeResumeLockExcludesAnotherProcess(t *testing.T) {
	if home := os.Getenv("AGENTHAIL_HOLD_RESUME_LOCK"); home != "" {
		unlock, err := NewClaude(nil, home).lockConversation(context.Background(), fixtureConversation)
		if err != nil {
			t.Fatal(err)
		}
		os.Stdout.WriteString("locked\n")
		_, _ = io.Copy(io.Discard, os.Stdin)
		unlock()
		return
	}
	home := t.TempDir()
	holder := exec.Command(os.Args[0], "-test.run=^TestClaudeResumeLockExcludesAnotherProcess$")
	holder.Env = append(os.Environ(), "AGENTHAIL_HOLD_RESUME_LOCK="+home)
	release, err := holder.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := holder.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := holder.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = holder.Process.Kill(); _ = holder.Wait() })
	if line, err := bufio.NewReader(output).ReadString('\n'); err != nil || line != "locked\n" {
		t.Fatalf("holder line=%q err=%v", line, err)
	}
	c := NewClaude(nil, home)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if _, err := c.lockConversation(ctx, fixtureConversation); err == nil {
		t.Fatal("took the resume lock while another process held it")
	}
	_ = release.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := c.lockConversation(ctx, fixtureConversation)
	if err != nil {
		t.Fatal(err)
	}
	unlock()
}

func TestClaudeCompactOfExitedSessionDoesNotResumeIt(t *testing.T) {
	fixture, host, _ := hostResume(t)
	session := exitedSession(fixture)
	c := NewClaudeWithRequest(signin.Fixed{Browser: signin.Chrome}, fixture.Home, func(context.Context, string, string, map[string]string, string, CookieSource, time.Duration) (int, string, error) {
		t.Fatal("compact was posted to a session without Remote Control")
		return 0, "", nil
	})
	if err := c.Compact(context.Background(), &session); !errors.Is(err, surface.ErrUnsupported) {
		t.Fatalf("err=%v, want unsupported", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("compact resumed the conversation: %+v", spawns)
	}
}

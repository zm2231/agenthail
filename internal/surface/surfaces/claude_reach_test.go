package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const exitedPID = 2147483647

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

func exitedSession(fixture *claudetest.Fixture) surface.Session {
	return surface.Session{ID: fixtureConversation, Bridge: "session_fixture", Surface: surface.KindClaude, PID: exitedPID, Status: surface.StatusBusy, Cwd: fixture.Home, Transcript: fixture.Transcript(fixtureConversation, fixture.Home)}
}

func TestClaudeConcurrentSendsToExitedSessionResumeItOnce(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	fixture.ResumeBridge("session_fixture")
	fixture.ResumeDelay(300 * time.Millisecond)
	posts := &claudePosts{}
	var wait sync.WaitGroup
	errs := make([]error, 2)
	for index, message := range []string{"first", "second"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			session := exitedSession(fixture)
			result, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, message)
			if err == nil && (!result.Accepted || session.PID != fixture.PID || session.ID != fixtureConversation || session.Bridge != "session_fixture") {
				t.Errorf("result=%+v session=%+v", result, session)
			}
			errs[index] = err
		}()
	}
	wait.Wait()
	if errs[0] != nil || errs[1] != nil {
		t.Fatalf("errs=%v", errs)
	}
	if resumes := fixture.Invocations("--bg", "--resume", fixtureConversation); resumes != 1 || posts.count("first") != 1 || posts.count("second") != 1 {
		t.Fatalf("resumes=%d first=%d second=%d", resumes, posts.count("first"), posts.count("second"))
	}
}

func TestClaudeSendHoldsWhileResumedJobIsStartingWithoutRecord(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "starting", 0)
	posts := &claudePosts{}
	c := NewClaudeWithRequest("", fixture.Home, posts.request)
	session := exitedSession(fixture)
	observation, err := c.Observe(context.Background(), &session)
	if err != nil || observation.Status != surface.StatusOffline {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if _, err := c.Send(context.Background(), &session, "hello"); !surface.IsDeliveryHeld(err) {
		t.Fatalf("err=%v", err)
	}
	if fixture.Invocations("--bg", "--resume", fixtureConversation) != 0 || posts.count("hello") != 0 {
		t.Fatalf("resumed or delivered while the job was starting")
	}
}

func TestClaudeSendHoldsForLiveProcessWithoutMessaging(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Record(fixtureConversation, fixture.Home, "")
	posts := &claudePosts{}
	session := exitedSession(fixture)
	if _, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, "hello"); !surface.IsDeliveryHeld(err) {
		t.Fatalf("err=%v", err)
	}
	if fixture.Invocations("agents", "--json", "--all") != 0 || fixture.Invocations("--bg", "--resume", fixtureConversation) != 0 || posts.count("hello") != 0 {
		t.Fatal("a held conversation was resumed or delivered")
	}
}

func TestClaudeObserveExitedSessionIsAtRestOnlyWithoutLiveProcess(t *testing.T) {
	fixture := claudetest.New(t)
	c := NewClaude("", fixture.Home)
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

func TestClaudeResumeThatStartsACopyStopsItAndIsUnknown(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	fixture.ResumeCopy("90b6ce14")
	posts := &claudePosts{}
	session := exitedSession(fixture)
	_, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, "hello")
	if !surface.IsDeliveryOutcomeUnknown(err) || !strings.Contains(err.Error(), "copy 90b6ce14") {
		t.Fatalf("err=%v", err)
	}
	if fixture.Invocations("stop", "90b6ce14") != 1 || posts.count("hello") != 0 {
		t.Fatalf("stops=%d posts=%d", fixture.Invocations("stop", "90b6ce14"), posts.count("hello"))
	}
}

func TestClaudeResumeThatNeverBecomesReachableIsUnknown(t *testing.T) {
	shortResumeSettle(t)
	fixture := claudetest.New(t)
	fixture.ResumeNeverReady()
	posts := &claudePosts{}
	session := exitedSession(fixture)
	_, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, "hello")
	if !surface.IsDeliveryOutcomeUnknown(err) || posts.count("hello") != 0 {
		t.Fatalf("err=%v posts=%d", err, posts.count("hello"))
	}
}

func TestClaudeSendResumesTerminalSessionInItsTranscriptCwd(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.ResumeBridge("session_resumed")
	posts := &claudePosts{}
	session := exitedSession(fixture)
	session.Cwd = ""
	result, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, "hello")
	if err != nil || !result.Accepted || session.ID != fixtureConversation || session.Bridge != "session_resumed" || !samePath(t, session.Cwd, fixture.Home) || posts.count("hello") != 1 {
		t.Fatalf("result=%+v session=%+v err=%v", result, session, err)
	}
	if fixture.Invocations("--bg", "--resume", fixtureConversation) != 1 {
		t.Fatal("terminal session was not resumed by its session id")
	}
}

func TestClaudeResumeOfBridgedSessionWaitsForRemoteControl(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	posts := &claudePosts{}
	session := exitedSession(fixture)
	go func() {
		time.Sleep(500 * time.Millisecond)
		fixture.Record(fixtureConversation, fixture.Home, "session_fixture")
	}()
	result, err := NewClaudeWithRequest("", fixture.Home, posts.request).Send(context.Background(), &session, "hello")
	if err != nil || !result.Accepted || session.ID != fixtureConversation || session.Bridge != "session_fixture" || session.Transport != "" || posts.count("hello") != 1 {
		t.Fatalf("result=%+v session=%+v err=%v", result, session, err)
	}
}

func TestClaudeResumeOfBridgedSessionUsesSocketWhenRemoteControlNeverReturns(t *testing.T) {
	shortResumeSettle(t)
	fixture := claudetest.New(t)
	fixture.Listen()
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	session := exitedSession(fixture)
	if _, err := NewClaude("", fixture.Home).reach(context.Background(), &session); err != nil || session.ID != fixtureConversation || session.Transport != "uds" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func samePath(t *testing.T, a, b string) bool {
	t.Helper()
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}

func TestClaudeResumeLockExcludesAnotherProcess(t *testing.T) {
	if home := os.Getenv("AGENTHAIL_HOLD_RESUME_LOCK"); home != "" {
		unlock, err := NewClaude("", home).lockConversation(context.Background(), fixtureConversation)
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
	c := NewClaude("", home)
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

func TestClaudeCompactOfExitedSessionResumesItFirst(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "done", 0)
	fixture.ResumeBridge("session_fixture")
	session := exitedSession(fixture)
	compacts := 0
	c := NewClaudeWithRequest("", fixture.Home, func(_ context.Context, _, _ string, _ map[string]string, body, _ string, _ string, _ time.Duration) (int, string, error) {
		var envelope struct {
			Events []struct {
				Payload struct {
					UUID string `json:"uuid"`
				} `json:"payload"`
			} `json:"events"`
		}
		if err := json.Unmarshal([]byte(body), &envelope); err != nil || len(envelope.Events) != 1 {
			t.Fatalf("body=%s err=%v", body, err)
		}
		compacts++
		f, err := os.OpenFile(session.Transcript, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Fprintf(f, "{\"type\":\"user\",\"uuid\":%q,\"message\":{\"content\":\"/compact\"}}\n{\"type\":\"system\",\"subtype\":\"compact_boundary\",\"content\":\"done\"}\n", envelope.Events[0].Payload.UUID)
		return 200, `{}`, nil
	})
	if err := c.Compact(context.Background(), &session); err != nil {
		t.Fatal(err)
	}
	if fixture.Invocations("--bg", "--resume", fixtureConversation) != 1 || compacts != 1 || session.PID != fixture.PID {
		t.Fatalf("resumes=%d compacts=%d session=%+v", fixture.Invocations("--bg", "--resume", fixtureConversation), compacts, session)
	}
}

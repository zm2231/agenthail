package surfaces

import (
	"context"
	"os"
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
	return surface.Session{ID: "session_fixture", Surface: surface.KindClaude, PID: exitedPID, Status: surface.StatusBusy, Cwd: fixture.Home, Transcript: fixture.Transcript(fixtureConversation, fixture.Home)}
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
			if err == nil && (!result.Accepted || session.PID != fixture.PID || session.ID != "session_fixture") {
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
	if err != nil || !result.Accepted || session.ID != "session_resumed" || session.Cwd != fixture.Home || posts.count("hello") != 1 {
		t.Fatalf("result=%+v session=%+v err=%v", result, session, err)
	}
	if fixture.Invocations("--bg", "--resume", fixtureConversation) != 1 {
		t.Fatal("terminal session was not resumed by its session id")
	}
}

func TestClaudeConversationOfBridgeIDComesFromItsJob(t *testing.T) {
	fixture := claudetest.New(t)
	fixture.JobState("fe758937", "done", "", time.Now())
	c := NewClaude("", fixture.Home)
	if got := c.conversationOf(&surface.Session{ID: "session_fixture"}); got != "" {
		t.Fatalf("conversation=%q without a job naming the bridge", got)
	}
	record := `{"state":"done","sessionId":"` + fixtureConversation + `","bridgeSessionId":"cse_fixture"}`
	if err := os.WriteFile(filepath.Join(fixture.Home, ".claude", "jobs", "fe758937", "state.json"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
	if got := c.conversationOf(&surface.Session{ID: "session_fixture"}); got != fixtureConversation {
		t.Fatalf("conversation=%q", got)
	}
}

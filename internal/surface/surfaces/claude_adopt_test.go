package surfaces

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestAdoptRefusesABusySessionAndLeavesItsProcessRunning(t *testing.T) {
	for status, want := range map[string]string{"busy": "a turn is running", "waiting": "waiting on a question", "shell": "background task"} {
		t.Run(status, func(t *testing.T) {
			fixture, host, c := hostResume(t)
			fixture.Transcript(fixtureConversation, fixture.Home)
			fixture.RecordStatus(fixtureConversation, fixture.Home, "", status, "", time.Time{})
			_, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true})
			var busy *surface.AdoptBusyError
			if !errors.As(err, &busy) || !strings.Contains(busy.Reason, want) {
				t.Fatalf("err=%v", err)
			}
			if !claudeProcessAlive(fixture.PID) {
				t.Fatal("a busy terminal process was stopped")
			}
			if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
				t.Fatalf("a refused adoption resumed the conversation: %+v", spawns)
			}
		})
	}
}

func TestAdoptStopsAnIdleTerminalProcessOnlyWithConsent(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "")
	_, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{})
	var consent *surface.AdoptConsentError
	if !errors.As(err, &consent) || consent.PID != fixture.PID {
		t.Fatalf("a terminal process was not asked about: %v", err)
	}
	if !claudeProcessAlive(fixture.PID) {
		t.Fatal("the terminal process was stopped without consent")
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("spawns=%+v", spawns)
	}
	result, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(result.Stopped, "terminal process") || result.Session == nil || !claudeHosted(result.Session) {
		t.Fatalf("result=%+v", result)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v, want one resume", spawns)
	}
	again, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{})
	if err != nil || !again.Unchanged {
		t.Fatalf("adopting a hosted conversation again: result=%+v err=%v", again, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("adopting a hosted conversation resumed it again: %+v", spawns)
	}
}

func TestAdoptResumesAConversationInTheConfigRootThatHoldsIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	fixture := claudetest.New(t)
	spare := fixture.AddRoot("spare")
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	c := NewClaude(nil, fixture.Home)
	fixture.Transcript(fixtureConversation, fixture.Home)
	result, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stopped != "" || result.Session == nil || !claudeHosted(result.Session) || claudeTranscriptRoot(result.Session.Transcript) != spare {
		t.Fatalf("result=%+v session=%+v", result, result.Session)
	}
	spawns := host.Spawns(t, fixtureConversation)
	if len(spawns) != 1 || spawns[0].ConfigDir == nil || *spawns[0].ConfigDir != spare {
		t.Fatalf("spawns=%+v, want one resume in %s", spawns, spare)
	}
}

func TestAdoptRefusesASessionWithABackgroundAgentRunning(t *testing.T) {
	fixture, host, c := hostResume(t)
	path := fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.RecordStatus(fixtureConversation, fixture.Home, "", "idle", "", time.Now().Add(-time.Second))
	now := time.Now().UTC().Add(time.Second)
	appendTranscript(t, path, mustJSON(t, backgroundCall(now, "call-agent", "Agent", map[string]any{"description": "Review the diff"}))+"\n"+
		mustJSON(t, backgroundResult(now, "call-agent", map[string]any{"isAsync": true, "agentId": "a-review"}))+"\n")
	_, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true})
	var busy *surface.AdoptBusyError
	if !errors.As(err, &busy) || !strings.Contains(busy.Reason, "background task") {
		t.Fatalf("err=%v", err)
	}
	if !claudeProcessAlive(fixture.PID) || len(host.Spawns(t, fixtureConversation)) != 0 {
		t.Fatal("a session with background work was stopped or resumed")
	}
	appendTranscript(t, path, mustJSON(t, backgroundNotification(now.Add(time.Second), "a-review"))+"\n")
	if _, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true}); err != nil {
		t.Fatalf("adopting once the agent ended: %v", err)
	}
}

func TestAdoptRefusesABackgroundJobThatIsStillStarting(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "starting", 0)
	_, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true})
	var busy *surface.AdoptBusyError
	if !errors.As(err, &busy) || !strings.Contains(busy.Reason, "starting") {
		t.Fatalf("err=%v", err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 0 {
		t.Fatalf("a starting job's conversation was resumed again: %+v", spawns)
	}
}

func TestAdoptStopsARunningBackgroundJobThroughClaudeWithoutAskingAboutATerminal(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.BackgroundRecord(fixtureConversation, fixture.Home)
	fixture.Job("fe758937", fixtureConversation, fixture.Home, "running", 0)
	done, stopped := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(done); <-stopped })
	go func() {
		defer close(stopped)
		for fixture.Invocations("stop", "fe758937") == 0 {
			select {
			case <-done:
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
		fixture.RemoveRecord()
	}()
	result, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if result.Stopped != "background job fe758937" || result.Session == nil || !claudeHosted(result.Session) {
		t.Fatalf("result=%+v", result)
	}
	if !claudeProcessAlive(fixture.PID) {
		t.Fatal("the job's process was signalled instead of stopped through Claude")
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("spawns=%+v, want one resume", spawns)
	}
}

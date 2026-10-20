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

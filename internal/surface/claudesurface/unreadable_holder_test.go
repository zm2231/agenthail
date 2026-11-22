package claudesurface

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/claudesurface/claudetest"
)

// sharedWithAnUnreadableHolder returns two live holders whose later record is half written, and that record as it read.
func sharedWithAnUnreadableHolder(t *testing.T) (sharedConversationFixture, []byte) {
	t.Helper()
	shared := newSharedConversation(t)
	path := holderRecordPath(shared.fixture, shared.later)
	record, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"pid":`), 0o600); err != nil {
		t.Fatal(err)
	}
	return shared, record
}

func assertBusyNamingTheUnreadableHolder(t *testing.T, err error, shared sharedConversationFixture) {
	t.Helper()
	var busy *surface.AdoptBusyError
	if !errors.As(err, &busy) || !strings.Contains(busy.Reason, fmt.Sprintf("Claude process %d", shared.later.PID)) {
		t.Fatalf("err=%v, want it busy naming process %d", err, shared.later.PID)
	}
}

func assertBothHoldersRun(t *testing.T, shared sharedConversationFixture, action string) {
	t.Helper()
	for _, holder := range []*claudetest.Holder{shared.first, shared.later} {
		if err := syscall.Kill(holder.PID, 0); err != nil {
			t.Fatalf("a refused %s stopped process %d: %v", action, holder.PID, err)
		}
	}
}

func TestAMoveIsBusyWhileAnotherLiveProcessRecordIsUnreadable(t *testing.T) {
	shared, _ := sharedWithAnUnreadableHolder(t)
	session := &surface.Session{ID: sharedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	out, err := shared.adapter.PrepareMove(context.Background(), session, surface.AdoptRequest{StopTerminal: true})
	if err == nil {
		out.Release()
		t.Fatal("prepared a move while a live process that may hold the conversation could not be read")
	}
	assertBusyNamingTheUnreadableHolder(t, err, shared)
	assertBothHoldersRun(t, shared, "move")
}

func TestAnAdoptionIsBusyWhileAnotherLiveProcessRecordIsUnreadable(t *testing.T) {
	shared, _ := sharedWithAnUnreadableHolder(t)
	_, err := shared.adapter.AdoptSession(context.Background(), &surface.Session{ID: sharedConversation}, surface.AdoptRequest{StopTerminal: true})
	assertBusyNamingTheUnreadableHolder(t, err, shared)
	assertBothHoldersRun(t, shared, "adoption")
}

func TestAMoveThatWaitsCountsTheHolderWhoseRecordReadsAgain(t *testing.T) {
	shared, record := sharedWithAnUnreadableHolder(t)
	go func() {
		time.Sleep(300 * time.Millisecond)
		_ = os.WriteFile(holderRecordPath(shared.fixture, shared.later), record, 0o600)
	}()
	session := &surface.Session{ID: sharedConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	out, err := shared.adapter.PrepareMove(context.Background(), session, surface.AdoptRequest{StopTerminal: true, Wait: 10 * time.Second})
	if err == nil {
		out.Release()
		t.Fatal("prepared a move of a conversation two processes hold")
	}
	if !surface.IsDeliveryRefused(err) || !strings.Contains(err.Error(), "close the others first") {
		t.Fatalf("err=%v, want the move refused for its two holders", err)
	}
	assertBothHoldersRun(t, shared, "move")
}

func TestAdoptingAHostedConversationIsUnchangedWhileAnotherProcessRecordIsUnreadable(t *testing.T) {
	fixture, host, c := hostResume(t)
	fixture.Transcript(fixtureConversation, fixture.Home)
	fixture.Record(fixtureConversation, fixture.Home, "")
	if _, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{StopTerminal: true}); err != nil {
		t.Fatal(err)
	}
	other := fixture.Holder("conversation-other", fixture.Home)
	if err := os.WriteFile(holderRecordPath(fixture, other), []byte(`{"pid":`), 0o600); err != nil {
		t.Fatal(err)
	}
	again, err := c.AdoptSession(context.Background(), &surface.Session{ID: fixtureConversation}, surface.AdoptRequest{})
	if err != nil || !again.Unchanged {
		t.Fatalf("adopting a hosted conversation again: result=%+v err=%v", again, err)
	}
	if spawns := host.Spawns(t, fixtureConversation); len(spawns) != 1 {
		t.Fatalf("adopting a hosted conversation resumed it again: %+v", spawns)
	}
	if err := syscall.Kill(other.PID, 0); err != nil {
		t.Fatalf("the other process was stopped: %v", err)
	}
}

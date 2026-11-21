package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestARestartDoesNotWaitOnWordsReclassifiedAsNotThePersons(t *testing.T) {
	stub := &sorterStub{answer: fileTo("new")}
	_, r, handler, p, _ := sorterFixture(t, stub)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: p.session(t, r, "claude-scheduled").ID, PromptID: "scheduled", Prompt: "a scheduled prompt"})
	if done, err := p.store.ReclassifyUtterance(p.key, ledger.PromptRef("scheduled"), "event: scheduled task", time.Now()); err != nil || !done {
		t.Fatalf("reclassify done=%v err=%v", done, err)
	}
	restarted := newTestDaemon(t, r, []surface.Surface{&daemonSurface{sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}})
	restarted.contexts.wait = time.Hour
	recovered, _ := restarted.startMessageContexts(t.Context())
	select {
	case <-recovered:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery at start still waits on a message reclassified as not the person's")
	}
}

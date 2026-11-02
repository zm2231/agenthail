package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestAScheduledPromptIsNotThePersonsWordsInEitherOrder(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-wake", "wake-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "looping")
	source := journalSource(d, s, nil)
	now := time.Now()
	scheduled := func(promptID string) surface.TimelineItem {
		return surface.TimelineItem{ID: "event-" + promptID, Kind: "event", Title: "Claude Code instructions", PromptID: promptID, Timestamp: now.UTC().Format(time.RFC3339Nano)}
	}

	if !ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "wake-1", Prompt: "Check the verdict; continue the loop."}).Captured {
		t.Fatal("the hook capture should land before the journal says otherwise")
	}
	source.append(liveEvent(scheduled("wake-1")))

	source.append(liveEvent(scheduled("wake-2")))
	if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "wake-2", Prompt: "Check again."}).Captured {
		t.Fatal("a prompt the journal already named as scheduled was captured")
	}

	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "typed-1", Prompt: "ship it"})
	source.append(liveEvent(typedPrompt("typed-1", "typed-1", "ship it", now)))

	unattached, err := p.store.UnattachedFor(p.key, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(unattached) != 1 || unattached[0].ID != ledger.PromptRef("typed-1") {
		t.Fatalf("person words after a scheduled prompt: %+v", unattached)
	}
	var reclassified int
	for _, e := range p.events(t) {
		if e.Type == ledger.EventReclassified {
			reclassified++
		}
	}
	if reclassified != 1 {
		t.Fatalf("want one reclassification, got %d", reclassified)
	}
}

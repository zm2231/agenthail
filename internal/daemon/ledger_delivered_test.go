package daemon

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
)

func TestDeliveredWordsAndTheirPromptHookAreOneUtterance(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-once", "once-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	host := p.session(t, r, "hosted")
	bridge := p.session(t, r, "bridged")

	hostIntent := uuid.NewString()
	d.personCapture(delivery.PersonCaptureContext{Target: host, IntentUUID: hostIntent, Message: "ship it"})
	resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: host.ID, PromptID: "claude-own-id", Prompt: "ship it", MessageUUID: hostIntent})
	if !resp.Captured || resp.Ref != ledger.DeliveryRef(hostIntent) {
		t.Fatalf("the hook did not name the delivered utterance: %+v", resp)
	}

	bridgeIntent := uuid.NewString()
	d.personCapture(delivery.PersonCaptureContext{Target: bridge, IntentUUID: bridgeIntent, Message: "also this"})
	resp = ledgerPost(t, handler, ledgerCaptureRequest{SessionID: bridge.ID, PromptID: "bridge-prompt", Prompt: "also this\n"})
	if !resp.Captured || resp.Ref != ledger.DeliveryRef(bridgeIntent) {
		t.Fatalf("a hook without the uuid did not match the delivered text: %+v", resp)
	}
	resp = ledgerPost(t, handler, ledgerCaptureRequest{SessionID: bridge.ID, PromptID: "typed-again", Prompt: "also this"})
	if resp.Ref != ledger.PromptRef("typed-again") {
		t.Fatalf("a delivery was claimed twice: %+v", resp)
	}

	now := time.Now()
	journalSource(d, host, nil).append(liveEvent(typedPrompt(hostIntent, "claude-own-id", "ship it", now)))
	journalSource(d, bridge, nil).append(liveEvent(typedPrompt(bridgeIntent, "bridge-prompt", "also this", now)))
	journalSource(d, bridge, nil).append(liveEvent(typedPrompt(uuid.NewString(), "typed-again", "also this", now)))

	events := p.events(t)
	refs := map[string]int{}
	for _, e := range events {
		refs[e.Ref]++
	}
	if len(events) != 3 || refs[ledger.DeliveryRef(hostIntent)] != 1 || refs[ledger.DeliveryRef(bridgeIntent)] != 1 || refs[ledger.PromptRef("typed-again")] != 1 {
		t.Fatalf("want one utterance per message, got %+v", refs)
	}
}

func TestAPeerEnvelopeIsNeverCapturedAsThePersonsWords(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-peer", "peer-key")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	s := p.session(t, r, "worker")
	for _, prompt := range []string{"[from @lead] do the thing", "[from Agenthail CLI, no reply route] status?", "[from @lead via #ops] heads up"} {
		if ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: prompt, Prompt: prompt}).Captured {
			t.Fatalf("captured a peer message as the person's: %q", prompt)
		}
	}
}

func TestDeliveredWordsMatchByTextOnlyWhileRecent(t *testing.T) {
	var w deliveredWords
	start := time.Now()
	w.add("s", "u-old", "same", start)
	w.add("s", "u-new", "same", start.Add(time.Minute))
	if got, ok := w.claim("s", "", "same", start.Add(deliveredTextWindow+30*time.Second)); !ok || got != "u-new" {
		t.Fatalf("claim = %q %v, want the recent delivery only", got, ok)
	}
	if _, ok := w.claim("s", "", "same", start.Add(deliveredTextWindow+30*time.Second)); ok {
		t.Fatal("an expired delivery matched by text")
	}
	if got, ok := w.claim("s", "u-old", "different", start.Add(time.Hour)); !ok || got != "u-old" {
		t.Fatalf("the uuid claim is not bound by the text window: %q %v", got, ok)
	}
	if _, ok := w.claim("other", "", "same", start); ok {
		t.Fatal("matched another session's delivery")
	}
	for i := range deliveredPerSession + 1 {
		w.add("full", uuid.NewString(), "x", start.Add(time.Duration(i)))
	}
	if len(w.bySession["full"]) != deliveredPerSession {
		t.Fatalf("kept %d entries for one session", len(w.bySession["full"]))
	}
}

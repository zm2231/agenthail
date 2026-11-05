package daemon

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
)

func TestDeliveredWordsAndTheirPromptHookAreOneUtterance(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-once", "once-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
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
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
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
	if got, _, ok := w.claim("s", "p1", "", "same", start.Add(deliveredTextWindow+30*time.Second)); !ok || got != "u-new" {
		t.Fatalf("claim = %q %v, want the recent delivery only", got, ok)
	}
	if _, _, ok := w.claim("s", "p2", "", "same", start.Add(deliveredTextWindow+30*time.Second)); ok {
		t.Fatal("an expired delivery matched by text")
	}
	if got, _, ok := w.claim("s", "p3", "u-old", "different", start.Add(time.Hour)); !ok || got != "u-old" {
		t.Fatalf("the uuid claim is not bound by the text window: %q %v", got, ok)
	}
	if _, _, ok := w.claim("other", "p4", "", "same", start); ok {
		t.Fatal("matched another session's delivery")
	}
	for i := range deliveredPerSession + 1 {
		w.add("full", uuid.NewString(), "x", start.Add(time.Duration(i)))
	}
	if len(w.bySession["full"]) != deliveredPerSession {
		t.Fatalf("kept %d entries for one session", len(w.bySession["full"]))
	}
}

// A Claude host runs its own copy of the prompt hook and also loads the
// person's settings, so the copy installed there reports the same prompt.
func TestEveryHookCopyOfADeliveredPromptNamesTheDelivery(t *testing.T) {
	for _, order := range []struct {
		name  string
		first string
	}{{"host copy first", "with-uuid"}, {"installed copy first", "without-uuid"}} {
		t.Run(order.name, func(t *testing.T) {
			d, r, handler := ledgerDaemon(t)
			p := addLedgerProject(t, r, "proj-copies", "copies-key")
			if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
				t.Fatal(err)
			}
			hosted := p.session(t, r, "hosted")
			intent := uuid.NewString()
			d.personCapture(delivery.PersonCaptureContext{Target: hosted, IntentUUID: intent, Message: "ship it"})
			copies := map[string]ledgerCaptureRequest{
				"with-uuid":    {SessionID: hosted.ID, PromptID: "claude-prompt", Prompt: "ship it", MessageUUID: intent},
				"without-uuid": {SessionID: hosted.ID, PromptID: "claude-prompt", Prompt: "ship it"},
			}
			second := "without-uuid"
			if order.first == second {
				second = "with-uuid"
			}
			for _, copy := range []string{order.first, second} {
				if resp := ledgerPost(t, handler, copies[copy]); !resp.Captured || resp.Ref != ledger.DeliveryRef(intent) {
					t.Fatalf("the %s hook copy named %q, want the delivery", copy, resp.Ref)
				}
			}
			if events := p.events(t); len(events) != 1 {
				t.Fatalf("one delivered prompt recorded %d utterances", len(events))
			}
		})
	}
}

func TestEveryHookCopyOfATypedPromptIsOneUtterance(t *testing.T) {
	_, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-typed", "typed-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	terminal := p.session(t, r, "terminal")
	for range 2 {
		if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: terminal.ID, PromptID: "typed", Prompt: "ship it"}); resp.Ref != ledger.PromptRef("typed") {
			t.Fatalf("a typed prompt named %q", resp.Ref)
		}
	}
	if events := p.events(t); len(events) != 1 {
		t.Fatalf("one typed prompt recorded %d utterances", len(events))
	}
}

func TestDeliveredWordsAreForgottenOnceNoHookCanStillAsk(t *testing.T) {
	var w deliveredWords
	start := time.Now()
	for i := range 1000 {
		w.add(fmt.Sprintf("s-%d", i), uuid.NewString(), "never seen", start)
	}
	w.add("hosted", "u-1", "ship it", start)
	if got, _, ok := w.claim("hosted", "prompt", "", "ship it", start.Add(time.Second)); !ok || got != "u-1" {
		t.Fatalf("claim = %q %v", got, ok)
	}
	if got, _, ok := w.claim("hosted", "prompt", "u-1", "ship it", start.Add(deliveredTextWindow)); !ok || got != "u-1" {
		t.Fatalf("another copy of the hook within the window = %q %v", got, ok)
	}
	w.add("later", uuid.NewString(), "new", start.Add(deliveredRetention+time.Minute))
	if len(w.bySession) != 1 {
		t.Fatalf("kept %d sessions' deliveries past the time any hook could claim them", len(w.bySession))
	}
}

func TestAFirstTurnWhoseStartFailedIsNeverClaimedByItsRetry(t *testing.T) {
	var w deliveredWords
	start := time.Now()
	w.addStart("failed", "[started] hello", "hello", start)
	w.settleStart("failed", "")
	w.addStart("retried", "[started] hello", "hello", start.Add(time.Second))
	w.settleStart("retried", "lead")
	if got, words, ok := w.claim("lead", "prompt", "", "[started] hello", start.Add(2*time.Second)); !ok || got != "retried" || words != "hello" {
		t.Fatalf("claim = %q %q %v, want the retried start", got, words, ok)
	}
	if _, _, ok := w.claim("other", "elsewhere", "", "[started] hello", start.Add(2*time.Second)); ok {
		t.Fatal("another session's prompt claimed a settled first turn")
	}
}

func TestAFirstTurnStillStartingIsNotTakenByAnotherSessionsPrompt(t *testing.T) {
	var w deliveredWords
	start := time.Now()
	w.addStart("starting", "[started] hello", "hello", start)
	if _, _, ok := w.claim("terminal", "typed", "", "[started] hello", start.Add(time.Second)); ok {
		t.Fatal("another session's prompt took a first turn whose start had not returned")
	}
	if got, _, ok := w.claim("lead", "first", "starting", "[started] hello", start.Add(time.Second)); !ok || got != "starting" {
		t.Fatalf("the started session's hook = %q %v", got, ok)
	}
}

func TestAHookCopyThatRanBeforeItsStartSettledIsTakenBack(t *testing.T) {
	d, r, handler := ledgerDaemon(t)
	p := addLedgerProject(t, r, "proj-early", "early-key")
	if err := p.store.Enable(p.key, ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	lead := p.session(t, r, "lead")
	d.delivered.addStart("intent", "[started] hello", "hello", time.Now())
	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: lead.ID, PromptID: "first", Prompt: "[started] hello"}); resp.Ref != ledger.PromptRef("first") {
		t.Fatalf("the installed copy named %q before the start settled", resp.Ref)
	}
	if resp := ledgerPost(t, handler, ledgerCaptureRequest{SessionID: lead.ID, PromptID: "first", Prompt: "[started] hello", MessageUUID: "intent"}); resp.Ref != ledger.DeliveryRef("intent") {
		t.Fatalf("the host copy named %q", resp.Ref)
	}
	board, err := p.store.Board(p.key)
	if err != nil {
		t.Fatal(err)
	}
	if words := board.Utterances(); len(words) != 1 || words[0].Text != "hello" {
		t.Fatalf("the board holds %+v, want the person's words once", words)
	}
}

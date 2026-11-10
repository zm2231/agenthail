package daemon

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
)

func journal(t *testing.T, r *registry.Registry, sessionID, key string, entry map[string]any) {
	t.Helper()
	entry["itemId"], entry["version"], entry["op"] = key, 1, "upsert"
	data, _ := json.Marshal(entry)
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: sessionID, Kind: "message", ProviderKey: key, Payload: data, ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 64, Bytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
}

func at(offset time.Duration) string { return time.Now().Add(offset).UTC().Format(time.RFC3339Nano) }

func recordedContext(t *testing.T, p ledgerProject, ref string) (ledger.ContextPayload, bool) {
	t.Helper()
	c, ok, err := p.store.ContextFor(p.key, ref)
	if err != nil {
		t.Fatal(err)
	}
	return c, ok
}

func TestASortWaitsForTheSessionToShowTheMessageAndAsksWithTheReplyBeforeIt(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	d.contexts.wait = 10 * time.Second
	s := p.session(t, r, "claude-context")
	journal(t, r, s.ID, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "Should I add a search box?", "ts": at(-time.Minute)})
	runSorter(t, d)

	start := time.Now()
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "yes, add it"})
	if waited := time.Since(start); waited > 2*time.Second {
		t.Fatalf("capture took %s", waited)
	}
	time.Sleep(300 * time.Millisecond)
	if asked, recorded := stub.asks(), func() bool { _, ok := recordedContext(t, p, ledger.PromptRef("p-1")); return ok }(); len(asked) != 0 || recorded {
		t.Fatalf("asked %+v (context recorded %v) before the session showed the message", asked, recorded)
	}

	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": "yes, add it", "ts": at(0)})
	journal(t, r, s.ID, "r2", map[string]any{"kind": "message", "role": "assistant", "body": "Added it.", "ts": at(time.Second)})
	eventually(t, "the message to be asked about", func() bool { return len(stub.asks()) == 1 })
	if asked := stub.asks()[0]; asked.Reply != "Should I add a search box?" {
		t.Fatalf("asked with reply %q, want the one the message answers", asked.Reply)
	}
	c, _ := recordedContext(t, p, ledger.PromptRef("p-1"))
	if c.Status != ledger.ReplyFound || c.JoinedBy != ledger.JoinedByID || c.Reply == nil || c.Reply.ItemID != "r1" {
		t.Fatalf("context = %+v", c)
	}
}

func TestAMessageContextSaysWhetherTheReplyWasLateOrNeverThere(t *testing.T) {
	cases := map[string]struct {
		entries  func(t *testing.T, r *registry.Registry, sessionID string)
		status   string
		joinedBy string
		reply    string
	}{
		"the session never showed the message, a reply came before it": {
			entries: func(t *testing.T, r *registry.Registry, id string) {
				journal(t, r, id, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "Done.", "ts": at(-time.Minute)})
			},
			status: ledger.ReplyLate, joinedBy: ledger.JoinedByTime, reply: "Done.",
		},
		"the session never showed the message and has no reply": {
			entries: func(*testing.T, *registry.Registry, string) {},
			status:  ledger.ReplyLate,
		},
		"the message opens the session": {
			entries: func(t *testing.T, r *registry.Registry, id string) {
				journal(t, r, id, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": "start", "ts": at(0)})
			},
			status: ledger.ReplyNone, joinedBy: ledger.JoinedByID,
		},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			stub := &sorterStub{}
			d, r, handler, p, search := sorterFixture(t, stub)
			stub.setAnswer(fileTo(search))
			s := p.session(t, r, "claude-late")
			c.entries(t, r, s.ID)
			runSorter(t, d)
			ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "start"})
			eventually(t, "the message to be asked about", func() bool { return len(stub.asks()) == 1 })
			got, _ := recordedContext(t, p, ledger.PromptRef("p-1"))
			reply := ""
			if got.Reply != nil {
				reply = got.Reply.Text
			}
			if got.Status != c.status || got.JoinedBy != c.joinedBy || reply != c.reply || stub.asks()[0].Reply != c.reply {
				t.Fatalf("context = %+v, asked with %q", got, stub.asks()[0].Reply)
			}
		})
	}
}

func TestADeliveredMessageIsJoinedToItsEntryInTheSession(t *testing.T) {
	stub := &sorterStub{}
	d, r, _, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	d.contexts.wait = 10 * time.Second
	s := p.session(t, r, "claude-delivered")
	runSorter(t, d)
	d.personCapture(delivery.PersonCaptureContext{Target: s, IntentUUID: "intent-1", Message: "ship the fix"})
	journal(t, r, s.ID, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "The fix is ready; ship it?", "ts": at(-time.Minute)})
	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "origin": "person", "messageId": "intent-1", "body": "ship the fix", "ts": at(0)})
	eventually(t, "the delivered message to be asked about", func() bool { return len(stub.asks()) == 1 })
	c, _ := recordedContext(t, p, ledger.DeliveryRef("intent-1"))
	if c.Status != ledger.ReplyFound || c.JoinedBy != ledger.JoinedByID || c.Reply.Text != "The fix is ready; ship it?" {
		t.Fatalf("context = %+v", c)
	}
}

func TestALongReplyKeptOutsideTheJournalRowIsReadWhole(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	s := p.session(t, r, "claude-long")
	full := strings.Repeat("detail ", 1000) + "Which option do you want?"
	payload, _ := json.Marshal(map[string]any{"itemId": "r1", "version": 1, "op": "upsert", "kind": "message", "role": "assistant", "body": full[:100], "truncated": true, "bodyRef": "body-r1", "ts": at(-time.Minute)})
	if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.ID, Kind: "message", ProviderKey: "r1", Payload: payload, BodyRef: "body-r1", FullBody: []byte(full), ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: 64, Bytes: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": "the second", "ts": at(0)})
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: "the second"})
	eventually(t, "the message to be asked about", func() bool { return len(stub.asks()) == 1 })
	c, _ := recordedContext(t, p, ledger.PromptRef("p-1"))
	if c.Reply == nil || c.Reply.Runes != len(full) || !strings.HasSuffix(c.Reply.Text, "Which option do you want?") || !strings.HasSuffix(stub.asks()[0].Reply, "Which option do you want?") {
		t.Fatalf("context = %+v", c)
	}
}

func TestAReplyLongerThanTheContextKeepsItsEnd(t *testing.T) {
	long := make([]rune, ledger.ContextReplyRunes+500)
	for i := range long {
		long[i] = 'a'
	}
	full := string(long) + "Which option?"
	reply := ledger.NewReply("r1", at(0), full)
	if len([]rune(reply.Text)) != ledger.ContextReplyRunes || reply.Runes != len([]rune(full)) || reply.Text[len(reply.Text)-len("Which option?"):] != "Which option?" {
		t.Fatalf("kept %d runes of %d, ending %q", len([]rune(reply.Text)), reply.Runes, reply.Text[len(reply.Text)-20:])
	}
}

func TestTextThePersonQuotedFromEarlierRepliesIsRecordedAndMarkedForTheSorter(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	d.contexts.wait = 10 * time.Second
	s := p.session(t, r, "claude-quotes")
	journal(t, r, s.ID, "r1", map[string]any{"kind": "message", "role": "assistant", "body": "Plan: the export writes one CSV per project, streamed so large projects never load into memory.", "ts": at(-2 * time.Minute)})
	journal(t, r, s.ID, "r2", map[string]any{"kind": "message", "role": "assistant", "body": "The index is in. Anything else?", "ts": at(-time.Minute)})
	message := "> The index is in. Anything else?\nyes, the export writes one CSV per project, streamed so large projects never load -- make it a zip"
	journal(t, r, s.ID, "u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": message, "ts": at(0)})
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: message})
	eventually(t, "the message to be asked about", func() bool { return len(stub.asks()) == 1 })

	c, _ := recordedContext(t, p, ledger.PromptRef("p-1"))
	var from []string
	for _, q := range c.Quotes {
		from = append(from, q.ReplyItemID)
	}
	if strings.Join(from, ",") != "r2,r1" || ledger.Typed(message, c.Quotes) != "yes,\n-- make it a zip" {
		t.Fatalf("quotes %+v from %v, typed %q", c.Quotes, from, ledger.Typed(message, c.Quotes))
	}
	want := "> The index is in. Anything else?\nyes,\n> the export writes one CSV per project, streamed so large projects never load\n-- make it a zip"
	if asked := stub.asks()[0]; asked.Message != want || asked.Reply != "The index is in. Anything else?" {
		t.Fatalf("asked %q with reply %q, want the quotes marked", asked.Message, asked.Reply)
	}
}

func TestAQuoteOfAReplyFarBackInALongSessionIsStillFound(t *testing.T) {
	stub := &sorterStub{}
	d, r, handler, p, search := sorterFixture(t, stub)
	stub.setAnswer(fileTo(search))
	d.contexts.wait = 10 * time.Second
	s := p.session(t, r, "claude-long-session")
	add := func(key string, entry map[string]any) {
		entry["itemId"], entry["version"], entry["op"] = key, 1, "upsert"
		data, _ := json.Marshal(entry)
		if _, _, err := r.AppendSessionJournalEntry(registry.SessionJournalEntry{SessionID: s.ID, Kind: "message", ProviderKey: key, Payload: data, ObservedAt: time.Now()}, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
			t.Fatal(err)
		}
	}
	add("r-old", map[string]any{"kind": "message", "role": "assistant", "body": "The cache keeps one entry per user and evicts the least recently used after an hour.", "ts": at(-time.Hour)})
	for i := range 900 {
		add(fmt.Sprintf("t%d", i), map[string]any{"kind": "tool", "role": "tool", "body": "ok", "ts": at(-30 * time.Minute)})
	}
	add("r-new", map[string]any{"kind": "message", "role": "assistant", "body": "Done. Anything else?", "ts": at(-time.Minute)})
	message := "earlier you said: one entry per user and evicts the least recently used after an hour. make it a day"
	add("u1", map[string]any{"kind": "message", "role": "user", "promptId": "p-1", "body": message, "ts": at(0)})
	runSorter(t, d)
	ledgerPost(t, handler, ledgerCaptureRequest{SessionID: s.ID, PromptID: "p-1", Prompt: message})
	eventually(t, "the message to be asked about", func() bool { return len(stub.asks()) == 1 })
	c, _ := recordedContext(t, p, ledger.PromptRef("p-1"))
	if len(c.Quotes) != 1 || c.Quotes[0].ReplyItemID != "r-old" || c.Reply == nil || c.Reply.ItemID != "r-new" {
		t.Fatalf("context = %+v", c)
	}
}

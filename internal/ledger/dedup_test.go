package ledger_test

import (
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

func newStore(t *testing.T) *ledger.Store {
	db := openTestDB(t)
	return ledger.NewCachedStore(db, db)
}

func said(ref, text string) ledger.Utterance {
	return ledger.Utterance{ProjectKey: "p", Ref: ref, SpeakerID: "person", TargetID: "s1", Surface: "terminal", Text: text, CapturedBy: ledger.CapturedByHook}
}

func utterances(t *testing.T, s *ledger.Store) []ledger.Event {
	t.Helper()
	events, err := s.Events("p", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func TestEachOccurrenceIsItsOwnUtterance(t *testing.T) {
	s := newStore(t)
	for _, u := range []ledger.Utterance{said(ledger.PromptRef("a"), "yes"), said(ledger.PromptRef("b"), "yes"), said(ledger.DeliveryRef("c"), "yes")} {
		if _, _, err := s.RecordUtterance(u); err != nil {
			t.Fatal(err)
		}
	}
	if got := utterances(t, s); len(got) != 3 {
		t.Fatalf("same words, three occurrences: want 3 utterances, got %d", len(got))
	}
}

func TestOneOccurrenceRecordedTwiceIsOneUtterance(t *testing.T) {
	s := newStore(t)
	first, _, err := s.RecordUtterance(said(ledger.PromptRef("a"), "yes"))
	if err != nil {
		t.Fatal(err)
	}
	again, _, err := s.RecordUtterance(said(ledger.PromptRef("a"), "yes, from the journal"))
	if err != nil {
		t.Fatal(err)
	}
	got := utterances(t, s)
	if len(got) != 1 || again.Seq != first.Seq || got[0].Ref != ledger.PromptRef("a") {
		t.Fatalf("want one utterance returned to both writers, got %+v (first %d again %d)", got, first.Seq, again.Seq)
	}
}

func TestConcurrentWritersOfOneOccurrenceRecordOne(t *testing.T) {
	s := newStore(t)
	const writers = 8
	seqs := make([]int64, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, _, err := s.RecordUtterance(said(ledger.PromptRef("same"), "build it"))
			seqs[i], errs[i] = e.Seq, err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil || seqs[i] != seqs[0] || seqs[i] == 0 {
			t.Fatalf("writer %d: seq %d (writer 0: %d) err %v", i, seqs[i], seqs[0], err)
		}
	}
	if got := utterances(t, s); len(got) != 1 {
		t.Fatalf("want one utterance, got %d", len(got))
	}
}

func TestOnlyAJournalFirstPromptIsAMissedHook(t *testing.T) {
	journal := said(ledger.PromptRef("a"), "yes")
	journal.CapturedBy = ledger.CapturedByJournal
	delivered := said(ledger.DeliveryRef("b"), "yes")
	delivered.CapturedBy = ledger.CapturedByJournal
	for name, got := range map[string]bool{
		"journal inserted a prompt":   journal.HookMissed(true),
		"journal found the prompt":    journal.HookMissed(false),
		"hook inserted the prompt":    said(ledger.PromptRef("a"), "yes").HookMissed(true),
		"journal inserted a delivery": delivered.HookMissed(true),
	} {
		if got != (name == "journal inserted a prompt") {
			t.Fatalf("%s: HookMissed=%v", name, got)
		}
	}
}

func TestAReplayedOriginIsAcceptedOnce(t *testing.T) {
	db := openTestDB(t)
	insert := `INSERT INTO ledger_events(project_key,type,payload,actor_host_id,origin_seq) VALUES('p','goal','{}',?,?)`
	if _, err := db.Exec(insert, "mac", 7); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(insert, "mac", 7); err == nil {
		t.Fatal("the same host's event 7 was accepted twice")
	}
	for _, row := range []struct {
		host string
		seq  int
	}{{"vps", 7}, {"mac", 8}, {"mac", 0}, {"mac", 0}} {
		if _, err := db.Exec(insert, row.host, row.seq); err != nil {
			t.Fatalf("host %s seq %d: %v", row.host, row.seq, err)
		}
	}
}

func TestUtteranceNeedsARef(t *testing.T) {
	if _, _, err := newStore(t).RecordUtterance(said("", "no ref")); err == nil {
		t.Fatal("an utterance without a ref was recorded")
	}
}

func TestEnableKeepsItsGrantTimeUntilDisabled(t *testing.T) {
	s := newStore(t)
	if scope, err := s.Scope("p"); err != nil || scope.State != ledger.LedgerUnset {
		t.Fatalf("new project scope %+v err %v", scope, err)
	}
	before := time.Now().Add(-time.Millisecond)
	if err := s.Enable("p", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	first, _ := s.Scope("p")
	if first.State != ledger.LedgerEnabled || first.Since.Before(before.Truncate(time.Millisecond)) {
		t.Fatalf("enable scope %+v", first)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.Enable("p", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if again, _ := s.Scope("p"); !again.Since.Equal(first.Since) {
		t.Fatalf("enabling an enabled ledger moved its grant from %v to %v", first.Since, again.Since)
	}
	if err := s.Disable("p"); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	if err := s.Enable("p", ledger.AttachEachPrompt); err != nil {
		t.Fatal(err)
	}
	if reenabled, _ := s.Scope("p"); !reenabled.Since.After(first.Since) {
		t.Fatalf("re-enable after disable kept the old grant %v", reenabled.Since)
	}
}

func TestConcurrentEnablesWriteOneCaptureEvent(t *testing.T) {
	db := openTestDB(t)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := ledger.NewStore(db, db).Enable("p", ledger.AttachEachPrompt); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key='p' AND type='capture'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("capture events = %d err %v, want 1", n, err)
	}
}

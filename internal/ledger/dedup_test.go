package ledger_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

var base = time.UnixMilli(1_800_000_000_000)

func hook(ref, target, text string, at time.Time) ledger.Utterance {
	return ledger.Utterance{ProjectKey: "p", CaptureRef: ref, SpeakerID: "person", TargetID: target, Surface: "terminal", Text: text, At: at}
}

func journal(ref, target, text string, at time.Time) ledger.Utterance {
	return ledger.Utterance{ProjectKey: "p", JournalRef: ref, SpeakerID: "person", TargetID: target, Surface: "journal", Text: text, At: at}
}

func record(t *testing.T, s *ledger.Store, utterances ...ledger.Utterance) []ledger.Event {
	t.Helper()
	var out []ledger.Event
	for _, u := range utterances {
		e, err := s.RecordUtterance(u)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e)
	}
	return out
}

func utterances(t *testing.T, s *ledger.Store) []ledger.Event {
	t.Helper()
	events, err := s.Events("p", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	return events
}

func newStore(t *testing.T) *ledger.Store {
	db := openTestDB(t)
	return ledger.NewStore(db, db)
}

func TestSamePromptTwiceIsTwoUtterances(t *testing.T) {
	s := newStore(t)
	record(t, s, hook("h1", "s1", "yes", base), hook("h2", "s1", "yes", base.Add(time.Minute)))
	if got := utterances(t, s); len(got) != 2 {
		t.Fatalf("want 2 utterances, got %d", len(got))
	}
}

func TestBackfillOfBothRepeatsKeepsTwoAndBindsInOrder(t *testing.T) {
	s := newStore(t)
	captured := record(t, s, hook("h1", "s1", "yes", base), hook("h2", "s1", "yes", base.Add(time.Minute)))
	backfilled := record(t, s, journal("j1", "s1", "yes", base.Add(time.Second)), journal("j2", "s1", "yes", base.Add(time.Minute+time.Second)))
	got := utterances(t, s)
	if len(got) != 2 {
		t.Fatalf("want 2 utterances after backfill, got %d", len(got))
	}
	if backfilled[0].Seq != captured[0].Seq || backfilled[1].Seq != captured[1].Seq {
		t.Fatalf("journal entries bound out of order: captured %d,%d backfilled %d,%d", captured[0].Seq, captured[1].Seq, backfilled[0].Seq, backfilled[1].Seq)
	}
	if got[0].JournalRef != "j1" || got[1].JournalRef != "j2" || got[0].CaptureRef != "h1" || got[1].CaptureRef != "h2" {
		t.Fatalf("refs not recorded on the utterances: %+v", got)
	}
}

func TestBackfillBeforeHookStillOneUtterance(t *testing.T) {
	s := newStore(t)
	first := record(t, s, journal("j1", "s1", "ship it", base.Add(time.Second)))
	second := record(t, s, hook("h1", "s1", "ship it", base))
	got := utterances(t, s)
	if len(got) != 1 || first[0].Seq != second[0].Seq || got[0].CaptureRef != "h1" || got[0].JournalRef != "j1" {
		t.Fatalf("want one utterance carrying both refs, got %+v", got)
	}
}

func TestBackfillWithoutCaptureIsRecorded(t *testing.T) {
	s := newStore(t)
	record(t, s, hook("h1", "s1", "yes", base), journal("j1", "s1", "yes", base.Add(ledger.JournalWindow+time.Minute)), journal("j2", "s1", "no", base))
	if got := utterances(t, s); len(got) != 3 {
		t.Fatalf("journal prompts with no capture in the window must be kept, got %d utterances", len(got))
	}
}

func TestSameWordsToAnotherSessionAreSeparate(t *testing.T) {
	s := newStore(t)
	record(t, s, hook("h1", "s1", "yes", base), hook("h2", "s2", "yes", base), journal("j2", "s2", "yes", base))
	got := utterances(t, s)
	if len(got) != 2 || got[0].JournalRef != "" || got[1].JournalRef != "j2" {
		t.Fatalf("journal entry for s2 must bind only to s2's capture: %+v", got)
	}
}

func TestRetriedRefsAreIdempotent(t *testing.T) {
	s := newStore(t)
	record(t, s, hook("h1", "s1", "yes", base), hook("h1", "s1", "yes", base.Add(time.Minute)), journal("j1", "s1", "yes", base), journal("j1", "s1", "yes", base))
	if got := utterances(t, s); len(got) != 1 {
		t.Fatalf("retries of one ref must not add utterances, got %d", len(got))
	}
}

func TestConcurrentHookCopiesOfOneSubmissionRecordOne(t *testing.T) {
	s := newStore(t)
	const copies = 8
	seqs := make([]int64, copies)
	errs := make([]error, copies)
	var wg sync.WaitGroup
	for i := range copies {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := s.RecordUtterance(hook(fmt.Sprintf("copy-%d", i), "s1", "build it", base.Add(time.Duration(i)*100*time.Millisecond)))
			seqs[i], errs[i] = e.Seq, err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("copy %d: %v", i, err)
		}
		if seqs[i] != seqs[0] {
			t.Fatalf("copy %d got seq %d, copy 0 got %d", i, seqs[i], seqs[0])
		}
	}
	if got := utterances(t, s); len(got) != 1 {
		t.Fatalf("want one utterance for one submission, got %d", len(got))
	}
}

package ledger_test

import (
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
)

func TestConcurrentSameMessageIDRecordsOneUtterance(t *testing.T) {
	db := openTestDB(t)
	s := ledger.NewStore(db, db)
	const writers = 8
	seqs := make([]int64, writers)
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e, err := s.RecordUtterance("proj-1", "u-same", "person", "s1", "terminal", "build it")
			seqs[i], errs[i] = e.Seq, err
		}()
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
		if seqs[i] != seqs[0] {
			t.Fatalf("writer %d got seq %d, writer 0 got %d", i, seqs[i], seqs[0])
		}
	}
	events, err := s.Events("proj-1", 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].MessageID != "u-same" {
		t.Fatalf("want one utterance with message id u-same, got %+v", events)
	}
}

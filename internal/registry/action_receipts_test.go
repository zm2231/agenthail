package registry

import (
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestActionReceiptReservationIsDurableAndNamespaced(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reservation, err := first.ReserveActionReceipt("device-a", "same", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now())
	if err != nil || !reservation.Acquired {
		t.Fatalf("reservation=%+v err=%v", reservation, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	replay, err := second.ReserveActionReceipt("device-a", "same", "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", time.Now())
	if err != nil || replay.Acquired || replay.Receipt.Status != ActionReceiptPending {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
	other, err := second.ReserveActionReceipt("device-b", "same", "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", time.Now())
	if err != nil || !other.Acquired {
		t.Fatalf("other principal=%+v err=%v", other, err)
	}
}

func TestActionReceiptConcurrentReservationAcquiresOnce(t *testing.T) {
	r := openTestRegistry(t)
	const hash = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	var group sync.WaitGroup
	var mu sync.Mutex
	acquired := 0
	for i := 0; i < 12; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			reservation, err := r.ReserveActionReceipt("device-a", "concurrent", hash, time.Now())
			if err != nil {
				t.Errorf("reserve: %v", err)
				return
			}
			if reservation.Acquired {
				mu.Lock()
				acquired++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if acquired != 1 {
		t.Fatalf("acquired=%d, want 1", acquired)
	}
}

func TestActionReceiptCompletionIsBoundedAndDoesNotReopenReservation(t *testing.T) {
	r := openTestRegistry(t)
	const hash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	if _, err := r.ReserveActionReceipt("device-a", "bounded", hash, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.CompleteActionReceipt("device-a", "bounded", hash, []byte("receipt"), time.Now()); err != nil {
		t.Fatal(err)
	}
	replay, err := r.ReserveActionReceipt("device-a", "bounded", hash, time.Now())
	if err != nil || replay.Acquired || replay.Receipt.Status != ActionReceiptCompleted || string(replay.Receipt.Receipt) != "receipt" {
		t.Fatalf("completed=%+v err=%v", replay, err)
	}
	if err := r.CompleteActionReceipt("device-a", "bounded", hash, []byte("again"), time.Now()); err == nil {
		t.Fatalf("second completion err=%v", err)
	}
}

func TestActionReceiptRetryReopensOnlyForTheSameRequestAndKeepsFirstTime(t *testing.T) {
	r := openTestRegistry(t)
	const hash = "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee"
	const other = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	first := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if _, err := r.ReserveActionReceipt("device-a", "unanswered", hash, first); err != nil {
		t.Fatal(err)
	}
	if err := r.RetryActionReceipt("device-a", "unanswered", hash, first.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	mismatch, err := r.ReserveActionReceipt("device-a", "unanswered", other, first.Add(time.Minute))
	if err != nil || mismatch.Acquired || mismatch.Receipt.RequestHash != hash {
		t.Fatalf("different request=%+v err=%v", mismatch, err)
	}
	again, err := r.ReserveActionReceipt("device-a", "unanswered", hash, first.Add(time.Hour))
	if err != nil || !again.Acquired || again.Receipt.Status != ActionReceiptPending || again.Receipt.CreatedAt != formatTimestamp(first) {
		t.Fatalf("resend=%+v err=%v", again, err)
	}
	concurrent, err := r.ReserveActionReceipt("device-a", "unanswered", hash, first.Add(time.Hour))
	if err != nil || concurrent.Acquired {
		t.Fatalf("second resend while the first runs=%+v err=%v", concurrent, err)
	}
	if err := r.CompleteActionReceipt("device-a", "unanswered", hash, []byte("answer"), first.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
}

func TestActionReceiptPruneDeletesSettledBeforeCutoffInBatches(t *testing.T) {
	r := openTestRegistry(t)
	const hash = "abababababababababababababababababababababababababababababababab"
	cutoff := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	settle := func(key string, at time.Time, retry bool) {
		t.Helper()
		if _, err := r.ReserveActionReceipt("device-a", key, hash, at); err != nil {
			t.Fatal(err)
		}
		var err error
		if retry {
			err = r.RetryActionReceipt("device-a", key, hash, at)
		} else {
			err = r.CompleteActionReceipt("device-a", key, hash, []byte("done"), at)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, key := range []string{"old-1", "old-2", "old-3"} {
		settle(key, cutoff.Add(-time.Hour), false)
	}
	settle("old-retry", cutoff.Add(-time.Hour), true)
	settle("recent", cutoff.Add(time.Hour), false)
	if _, err := r.ReserveActionReceipt("device-a", "old-pending", hash, cutoff.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if removed, err := r.PruneActionReceipts(cutoff, 3); err != nil || removed != 3 {
		t.Fatalf("first batch removed=%d err=%v", removed, err)
	}
	if removed, err := r.PruneActionReceipts(cutoff, 3); err != nil || removed != 1 {
		t.Fatalf("second batch removed=%d err=%v", removed, err)
	}
	for key, kept := range map[string]bool{"old-1": false, "old-2": false, "old-3": false, "old-retry": false, "recent": true, "old-pending": true} {
		if _, found, err := r.ActionReceipt("device-a", key); err != nil || found != kept {
			t.Fatalf("%s found=%v err=%v, want %v", key, found, err, kept)
		}
	}
}

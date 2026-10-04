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

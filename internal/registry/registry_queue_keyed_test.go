package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCancelingByKeyOnlyWithdrawsAPendingItemAndRecordsWhy(t *testing.T) {
	r := openTestRegistry(t)
	for _, id := range []string{"pending", "inflight", "delivered"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}); err != nil {
			t.Fatal(err)
		}
		if _, err := r.QueueMessageWithKey(id, "notice for "+id, "key-"+id); err != nil {
			t.Fatal(err)
		}
	}
	claimed, err := r.ClaimNextMessage("inflight", time.Now())
	if err != nil || claimed == nil {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	delivered, err := r.ClaimNextMessage("delivered", time.Now())
	if err != nil || delivered == nil {
		t.Fatalf("delivered=%+v err=%v", delivered, err)
	}
	if err := r.AckMessage(delivered.ID); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]bool{"key-pending": true, "key-inflight": false, "key-delivered": false, "key-unknown": false, "": false} {
		if canceled, err := r.CancelPendingByKey(key); err != nil || canceled != want {
			t.Fatalf("%q: canceled=%v err=%v", key, canceled, err)
		}
	}
	if again, err := r.CancelPendingByKey("key-pending"); err != nil || again {
		t.Fatalf("a canceled item was canceled again: %v %v", again, err)
	}
	rows, err := r.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	statuses := map[string]string{}
	for _, row := range rows {
		statuses[row.SessionID] = row.Status
	}
	if statuses["pending"] != "canceled" || statuses["inflight"] != "inflight" || statuses["delivered"] != "delivered" {
		t.Fatalf("statuses=%v", statuses)
	}
	history, err := r.ListHistory(0, "pending")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, entry := range history {
		found = found || entry.Kind == "canceled" && entry.Result == "no longer needed"
	}
	if !found {
		t.Fatalf("history=%+v", history)
	}
	for _, key := range []string{"key-pending", "key-delivered"} {
		if used, err := r.DeliveryKeyUsed(key); err != nil || !used {
			t.Fatalf("%s: used=%v err=%v", key, used, err)
		}
	}
}

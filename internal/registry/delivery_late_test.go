package registry

import (
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARowDeferredPastTheBoundTellsItsSenderOnceAndStaysQueued(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "sender", "target")
	queueID, _ := queueClaimed(t, r, "are you there")
	now := time.Now()
	first := now.Add(-14 * time.Minute)
	if err := r.DeferMessage(queueID, errors.New("register Claude peer: sender is offline"), first); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncDeliveryAsks(now); err != nil {
		t.Fatal(err)
	}
	if asks := openDeliveryAsks(t, r); len(asks) != 0 {
		t.Fatalf("asks=%+v, want none inside the bound", asks)
	}
	if item, err := r.ClaimNextMessage("target", now); err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	if err := r.DeferMessage(queueID, errors.New("register Claude peer: sender is offline"), now); err != nil {
		t.Fatal(err)
	}
	row, err := r.QueueItem(queueID, nil)
	if err != nil || row.Status != "pending" || row.Deferrals != 2 || row.DeferredSinceMS != first.UnixMilli() || row.LastError == "" {
		t.Fatalf("row=%+v err=%v, want pending, deferred twice since the first deferral, with its reason", row, err)
	}
	later := now.Add(2 * time.Minute)
	if err := r.SyncDeliveryAsks(later); err != nil {
		t.Fatal(err)
	}
	ask := onlyDeliveryAsk(t, r)
	if ask.SessionID != "sender" || ask.RoutedTo != "sender" || ask.Ref != fmt.Sprintf("queue:%d:late", queueID) {
		t.Fatalf("ask=%+v, want the late row told to its sender", ask)
	}
	if ref := deliveryRef(t, ask); ref.QueueID != queueID || ref.Kind != "late" {
		t.Fatalf("ref=%+v", ref)
	}
	if got := askOptions(ask); !reflect.DeepEqual(got, []string{DeliveryCancel, DeliveryDismiss}) {
		t.Fatalf("options=%v", got)
	}
	if item, err := r.ClaimNextMessage("target", later.Add(time.Hour)); err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v, want the row still queued", item, err)
	}
	if err := r.SyncDeliveryAsks(later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if again := onlyDeliveryAsk(t, r); again.ID != ask.ID {
		t.Fatalf("ask=%s, want the same ask %s kept open while the row is retried", again.ID, ask.ID)
	}
	if err := r.AckMessage(queueID); err != nil {
		t.Fatal(err)
	}
	if err := r.SyncDeliveryAsks(later.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state := askState(t, r, ask.ID); state != AskWithdrawn {
		t.Fatalf("ask state=%q, want withdrawn once delivered", state)
	}
}

func TestALateRowFromASenderWithNoSessionAsksThePersonAboutTheTarget(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target")
	queueID, err := r.QueueMessageWithOptions("target", "from a sender this host never recorded", "", surface.SendOptions{SourceSessionID: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if item, err := r.ClaimNextMessage("target", now.Add(-20*time.Minute)); err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	if err := r.DeferMessage(queueID, errors.New("target is running but not reachable yet"), now.Add(-20*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if item, err := r.ClaimNextMessage("target", now); err != nil || item == nil || item.ID != queueID {
		t.Fatalf("claim=%+v err=%v, want the row claimed again past the bound", item, err)
	}
	ask := onlyDeliveryAsk(t, r)
	if ask.SessionID != "target" || ask.RoutedTo != AskToPerson || ask.Ref != fmt.Sprintf("queue:%d:late", queueID) {
		t.Fatalf("ask=%+v, want it on the target and routed to the person", ask)
	}
}

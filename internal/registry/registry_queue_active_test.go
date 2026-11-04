package registry

import (
	"testing"
	"time"
)

func TestActiveQueueLeavesOutFinishedRowsAndScopesToSession(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a", "b")
	if err := r.QueueMessage("a", "delivered"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("a", time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	if err := r.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	canceled, err := r.QueueMessageWithKey("a", "canceled", "canceled-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CancelMessage(canceled); err != nil {
		t.Fatal(err)
	}
	first, err := r.QueueMessageWithKey("a", "first", "first-a")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.QueueMessageWithKey("a", "second", "second-a")
	if err != nil {
		t.Fatal(err)
	}
	other, err := r.QueueMessageWithKey("b", "elsewhere", "pending-b")
	if err != nil {
		t.Fatal(err)
	}

	scoped, err := r.ActiveQueue("a", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 2 || scoped[0].ID != first || scoped[1].ID != second {
		t.Fatalf("scoped=%+v, want [%d %d]", scoped, first, second)
	}
	all, err := r.ActiveQueue("", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 || all[2].ID != other {
		t.Fatalf("all=%+v", all)
	}
}

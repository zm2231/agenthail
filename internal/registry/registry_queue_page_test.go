package registry

import (
	"testing"
	"time"
)

func deliverForPageTest(t *testing.T, r *Registry, sessionID, message string, age time.Duration) int64 {
	t.Helper()
	if err := r.QueueMessage(sessionID, message); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage(sessionID, time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	if err := r.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	if age > 0 {
		stamp := time.Now().Add(-age).UTC().Format(queuedAtLayout)
		if _, err := r.db.Exec(`UPDATE message_queue SET queued_at=? WHERE id=?`, stamp, item.ID); err != nil {
			t.Fatal(err)
		}
	}
	return item.ID
}

func pageIDs(rows []QueueRow) []int64 {
	ids := make([]int64, len(rows))
	for i, row := range rows {
		ids[i] = row.ID
	}
	return ids
}

func TestQueuePageCapsItsLimit(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a")
	for i := 0; i < queueHistoryPageMax+5; i++ {
		deliverForPageTest(t, r, "a", "row", 0)
	}
	page, err := r.QueuePage(QueuePageRequest{SessionID: "a", Limit: 10_000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.History) != queueHistoryPageMax || page.NextBefore == 0 {
		t.Fatalf("history=%d next=%d", len(page.History), page.NextBefore)
	}
	rest, err := r.QueuePage(QueuePageRequest{SessionID: "a", Before: page.NextBefore, Limit: 10_000}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rest.History) != 5 || rest.NextBefore != 0 {
		t.Fatalf("rest=%d next=%d", len(rest.History), rest.NextBefore)
	}
}

func TestQueuePageScopesToSessionAndWindowsHistory(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a")
	register(t, r, "b")
	register(t, r, "c")
	old := deliverForPageTest(t, r, "a", "old", 40*24*time.Hour)
	first := deliverForPageTest(t, r, "a", "first", 0)
	deliverForPageTest(t, r, "b", "other session", 0)
	second := deliverForPageTest(t, r, "a", "second", 0)
	pending, err := r.QueueMessageWithKey("a", "pending", "pending-a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithKey("b", "pending elsewhere", "pending-b"); err != nil {
		t.Fatal(err)
	}

	page, err := r.QueuePage(QueuePageRequest{SessionID: "a"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(page.Active); len(got) != 1 || got[0] != pending {
		t.Fatalf("active=%v, want [%d]", got, pending)
	}
	if got := pageIDs(page.History); len(got) != 2 || got[0] != second || got[1] != first {
		t.Fatalf("history=%v, want [%d %d]", got, second, first)
	}
	if page.NextBefore != first {
		t.Fatalf("nextBefore=%d, want %d", page.NextBefore, first)
	}

	older, err := r.QueuePage(QueuePageRequest{SessionID: "a", Before: page.NextBefore}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(older.History); len(older.Active) != 0 || len(got) != 1 || got[0] != old || older.NextBefore != 0 {
		t.Fatalf("older page active=%d history=%v next=%d", len(older.Active), got, older.NextBefore)
	}

	limited, err := r.QueuePage(QueuePageRequest{SessionID: "a", Limit: 1}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(limited.History); len(got) != 1 || got[0] != second || limited.NextBefore != second {
		t.Fatalf("limited history=%v next=%d", got, limited.NextBefore)
	}

	onlyOld := deliverForPageTest(t, r, "c", "only old", 60*24*time.Hour)
	empty, err := r.QueuePage(QueuePageRequest{SessionID: "c"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.History) != 0 || empty.NextBefore == 0 {
		t.Fatalf("window page history=%v next=%d", pageIDs(empty.History), empty.NextBefore)
	}
	rest, err := r.QueuePage(QueuePageRequest{SessionID: "c", Before: empty.NextBefore}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := pageIDs(rest.History); len(got) != 1 || got[0] != onlyOld || rest.NextBefore != 0 {
		t.Fatalf("rest=%v next=%d", got, rest.NextBefore)
	}
}

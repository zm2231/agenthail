package registry

import (
	"fmt"
	"strings"
	"testing"
)

func sourceErrorPayload(epoch, reason string) []byte {
	return fmt.Appendf(nil, `{"itemId":%q,"providerKey":%q,"version":1,"op":"reset","kind":"source-error","ts":"2026-11-25T00:00:00Z","reason":%q}`, epoch+":source", epoch+":source", reason)
}

func appendSourceError(t *testing.T, r *Registry, epoch, reason string, retention SessionJournalRetention) SessionJournalEntry {
	t.Helper()
	entry, changed, err := r.AppendSessionJournalSourceError(SessionJournalEntry{SessionID: "session", Kind: "source-error", ProviderKey: epoch + ":source", Payload: sourceErrorPayload(epoch, reason)}, retention)
	if err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	return entry
}

func TestSessionJournalPersistentSourceFailureKeepsHistory(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 5, Bytes: 8192}
	for index := 1; index <= 4; index++ {
		key := fmt.Sprintf("item-%d", index)
		if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(key)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	previous := appendSourceError(t, r, "epoch-1", "transcript is gone", retention)
	for epoch := 2; epoch <= 20; epoch++ {
		current := appendSourceError(t, r, fmt.Sprintf("epoch-%d", epoch), "transcript is gone", retention)
		if current.Seq <= previous.Seq || current.Position != previous.Position {
			t.Fatalf("epoch %d: previous=%+v current=%+v", epoch, previous, current)
		}
		follower, err := r.SessionJournalAfter("session", previous.Seq, 10)
		if err != nil || follower.Gap || len(follower.Entries) != 1 || follower.Entries[0].Seq != current.Seq {
			t.Fatalf("a follower at the previous failure must receive the repeat: %+v err=%v", follower, err)
		}
		previous = current
	}
	window, err := r.SessionJournalAfter("session", 0, 50)
	if err != nil || window.Gap || len(window.Entries) != 5 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	if window.Entries[0].ProviderKey != "item-1" || window.Entries[4].ProviderKey != "epoch-20:source" {
		t.Fatalf("entries=%+v", window.Entries)
	}
	var rows, bytes int
	if err := r.db.QueryRow(journalRetainedSQL, "session").Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if rows != 5 || bytes != len("item-1")*4+len(sourceErrorPayload("epoch-20", "transcript is gone")) {
		t.Fatalf("retained rows=%d bytes=%d", rows, bytes)
	}
}

func TestSessionJournalSourceFailureIsAddedOnceAnythingChanges(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 50, Bytes: 8192}
	first := appendSourceError(t, r, "a", "transcript is gone", retention)
	different := appendSourceError(t, r, "b", "the provider refused", retention)
	if different.Position == first.Position {
		t.Fatal("a different failure must be its own row")
	}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "done", ProviderKey: "turn-1:done", Payload: []byte("done")}, retention); err != nil {
		t.Fatal(err)
	}
	after := appendSourceError(t, r, "c", "the provider refused", retention)
	if after.Position == different.Position {
		t.Fatal("a failure after a newer row must be its own row")
	}
	page, err := r.ReadSessionJournalPage("session", 0, 10)
	if err != nil || len(page.Entries) != 4 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if newest := page.Entries[len(page.Entries)-1]; newest.ProviderKey != "c:source" {
		t.Fatalf("newest=%+v", newest)
	}
	if replay, err := r.SessionJournalAfter("session", 0, 10); err != nil || replay.Gap || len(replay.Entries) != 4 {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestSessionJournalRepeatedFailureAtAnOlderPositionIsAddedLast(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 50, Bytes: 8192}
	appendSourceError(t, r, "a", "transcript is gone", retention)
	item, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "item-1", Payload: []byte("item")}, retention)
	if err != nil {
		t.Fatal(err)
	}
	again, changed, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "source-error", ProviderKey: "a:source", Payload: []byte(strings.Replace(string(sourceErrorPayload("a", "transcript is gone")), `"version":1`, `"version":2`, 1))}, retention)
	if err != nil || !changed || again.Seq <= item.Seq || again.Position >= item.Position {
		t.Fatalf("A's second failure must be newest by sequence at its old position: again=%+v item=%+v changed=%v err=%v", again, item, changed, err)
	}
	appendSourceError(t, r, "b", "transcript is gone", retention)
	page, err := r.ReadSessionJournalPage("session", 0, 10)
	if err != nil || len(page.Entries) != 3 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if last := page.Entries[len(page.Entries)-1]; last.ProviderKey != "b:source" {
		t.Fatalf("the current failure must be the last row: %+v", page.Entries)
	}
}

func TestSessionJournalReplacedFailureKeepsRetention(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 50, Bytes: len(sourceErrorPayload("longer-epoch", "gone")) + 1}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "item-1", Payload: []byte("item-1")}, retention); err != nil {
		t.Fatal(err)
	}
	appendSourceError(t, r, "a", "gone", retention)
	appendSourceError(t, r, "longer-epoch", "gone", retention)
	var rows, bytes int
	if err := r.db.QueryRow(journalRetainedSQL, "session").Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if bytes > retention.Bytes || rows != 1 {
		t.Fatalf("retained rows=%d bytes=%d limit=%d", rows, bytes, retention.Bytes)
	}
	var prunedBefore, prunedSeq int64
	if err := r.db.QueryRow(`SELECT pruned_before,pruned_seq FROM session_journal_state WHERE session_id=?`, "session").Scan(&prunedBefore, &prunedSeq); err != nil {
		t.Fatal(err)
	}
	if prunedBefore != 1 || prunedSeq != 1 {
		t.Fatalf("pruned_before=%d pruned_seq=%d, want the trimmed item's 1 and 1", prunedBefore, prunedSeq)
	}
	if behind, err := r.SessionJournalAfter("session", 0, 10); err != nil || behind.Gap || len(behind.Entries) != 1 {
		t.Fatalf("fresh follower=%+v err=%v", behind, err)
	}
}

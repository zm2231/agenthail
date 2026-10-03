package registry

import (
	"bytes"
	"testing"
	"time"
)

func TestSessionJournalReplaysUpsertsAndReportsRetentionGap(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 2, Bytes: 32}
	first, inserted, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "item-1", Payload: []byte("one"), ObservedAt: time.Unix(1, 0)}, retention)
	if err != nil || !inserted || first.Seq != 1 {
		t.Fatalf("first=%+v inserted=%v err=%v", first, inserted, err)
	}
	updated, inserted, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "item-1", Payload: []byte("one updated"), ObservedAt: time.Unix(2, 0)}, retention)
	if err != nil || inserted || updated.Seq != first.Seq || !bytes.Equal(updated.Payload, []byte("one updated")) {
		t.Fatalf("updated=%+v inserted=%v err=%v", updated, inserted, err)
	}
	for _, key := range []string{"item-2", "item-3", "item-4"} {
		if _, inserted, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(key)}, retention); err != nil || !inserted {
			t.Fatalf("key=%s inserted=%v err=%v", key, inserted, err)
		}
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || window.Gap || len(window.Entries) != 2 || window.EarliestSeq != 3 || window.LatestSeq != 4 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	if window.Entries[0].ProviderKey != "item-3" || window.Entries[1].ProviderKey != "item-4" {
		t.Fatalf("entries=%+v", window.Entries)
	}
	gap, err := r.SessionJournalAfter("session", 1, 10)
	if err != nil || !gap.Gap || len(gap.Entries) != 0 {
		t.Fatalf("gap=%+v err=%v", gap, err)
	}
}

func TestSessionJournalRejectsEntryBeyondByteRetention(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", Payload: []byte("too large")}, SessionJournalRetention{Count: 1, Bytes: 3}); err == nil {
		t.Fatal("oversized entry was accepted")
	}
}

func TestSessionJournalUpsertMaintainsByteRetention(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 3, Bytes: 8}
	for _, input := range []SessionJournalEntry{
		{SessionID: "session", Kind: "item", ProviderKey: "first", Payload: []byte("one")},
		{SessionID: "session", Kind: "item", ProviderKey: "second", Payload: []byte("two")},
		{SessionID: "session", Kind: "item", ProviderKey: "second", Payload: []byte("second")},
	} {
		if _, _, err := r.AppendSessionJournalEntry(input, retention); err != nil {
			t.Fatal(err)
		}
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || len(window.Entries) != 1 || window.Entries[0].ProviderKey != "second" || window.Entries[0].Bytes != 6 {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

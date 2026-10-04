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
	if err != nil || inserted || updated.Seq <= first.Seq || !bytes.Equal(updated.Payload, []byte("one updated")) {
		t.Fatalf("updated=%+v inserted=%v err=%v", updated, inserted, err)
	}
	replayed, err := r.SessionJournalAfter("session", first.Seq, 10)
	if err != nil || len(replayed.Entries) != 1 || replayed.Entries[0].Seq != updated.Seq || !bytes.Equal(replayed.Entries[0].Payload, updated.Payload) {
		t.Fatalf("replayed=%+v err=%v", replayed, err)
	}
	for _, key := range []string{"item-2", "item-3", "item-4"} {
		if _, inserted, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(key)}, retention); err != nil || !inserted {
			t.Fatalf("key=%s inserted=%v err=%v", key, inserted, err)
		}
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || window.Gap || len(window.Entries) != 2 || window.EarliestSeq != 4 || window.LatestSeq != 5 {
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

func TestSessionJournalBodyIsSessionBoundAndExpiresWithRetention(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	register(t, r, "other")
	retention := SessionJournalRetention{Count: 1, Bytes: 1024}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "one", Payload: []byte(`{"bodyRef":"opaque"}`), BodyRef: "opaque", FullBody: []byte("abcdefghijklmnopqrstuvwxyz")}, retention); err != nil {
		t.Fatal(err)
	}
	body, total, err := r.SessionJournalBody("session", "opaque", 2, 8)
	if err != nil || string(body) != "cdefgh" || total != 26 {
		t.Fatalf("body=%q total=%d err=%v", body, total, err)
	}
	if _, _, err := r.SessionJournalBody("other", "opaque", 0, 1); err == nil {
		t.Fatal("cross-session body read succeeded")
	}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "two", Payload: []byte("two")}, retention); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SessionJournalBody("session", "opaque", 0, 1); err == nil {
		t.Fatal("trimmed body remained readable")
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

package registry

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
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
	if err != nil || !inserted || updated.Seq <= first.Seq || !bytes.Equal(updated.Payload, []byte("one updated")) {
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

func TestSessionJournalIdenticalProviderReplayDoesNotAdvanceWatermark(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 4, Bytes: 1024}
	payload := []byte(`{"itemId":"item-1","providerKey":"item-1","version":7,"kind":"text","ts":"2026-10-04T00:00:00Z","body":"same","bodyRef":"ref-a"}`)
	first, changed, err := r.AppendSessionJournalEntry(SessionJournalEntry{
		SessionID:   "session",
		Kind:        "text",
		ProviderKey: "item-1",
		Payload:     payload,
		BodyRef:     "ref-a",
		FullBody:    []byte("same full body"),
		ObservedAt:  time.Unix(1, 0),
	}, retention)
	if err != nil || !changed || first.Seq != 1 {
		t.Fatalf("first=%+v changed=%v err=%v", first, changed, err)
	}
	replayedPayload := []byte(`{"itemId":"item-1","providerKey":"item-1","version":7,"kind":"text","ts":"2026-10-04T00:00:00Z","body":"same","bodyRef":"ref-b"}`)
	replayed, changed, err := r.AppendSessionJournalEntry(SessionJournalEntry{
		SessionID:   "session",
		Kind:        "text",
		ProviderKey: "item-1",
		Payload:     replayedPayload,
		BodyRef:     "ref-b",
		FullBody:    []byte("same full body"),
		ObservedAt:  time.Unix(2, 0),
	}, retention)
	if err != nil || changed || replayed.Seq != first.Seq || !bytes.Equal(replayed.Payload, first.Payload) || replayed.BodyRef != first.BodyRef {
		t.Fatalf("replayed=%+v changed=%v err=%v", replayed, changed, err)
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || window.LatestSeq != first.Seq || len(window.Entries) != 1 || window.Entries[0].BodyRef != "ref-a" {
		t.Fatalf("window=%+v err=%v", window, err)
	}
	if _, _, err := r.SessionJournalBody("session", "ref-b", 0, 1); err == nil {
		t.Fatal("identical replay stored a new body reference")
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

func TestSessionJournalBodyRangesPreserveUTF8AcrossBoundaries(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	body := "A🙂BéC"
	retention := SessionJournalRetention{Count: 2, Bytes: 1024}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "unicode", Payload: []byte(`{"bodyRef":"unicode"}`), BodyRef: "unicode", FullBody: []byte(body)}, retention); err != nil {
		t.Fatal(err)
	}
	first, total, err := r.SessionJournalBody("session", "unicode", 0, 3)
	if err != nil || total != len(body) || string(first) != "A" {
		t.Fatalf("first=%q total=%d err=%v", first, total, err)
	}
	second, _, err := r.SessionJournalBody("session", "unicode", len(first), 5)
	if err != nil || string(second) != "🙂" {
		t.Fatalf("second=%q err=%v", second, err)
	}
	third, _, err := r.SessionJournalBody("session", "unicode", len(first)+len(second), len(body))
	if err != nil || string(third) != "BéC" {
		t.Fatalf("third=%q err=%v", third, err)
	}
	if joined := string(append(append(first, second...), third...)); joined != body || !utf8.ValidString(joined) {
		t.Fatalf("joined=%q valid=%v", joined, utf8.ValidString(joined))
	}
	if _, _, err := r.SessionJournalBody("session", "unicode", 2, 5); err == nil || !strings.Contains(err.Error(), "inside a UTF-8 code point") {
		t.Fatalf("interior start err=%v", err)
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

func TestSessionJournalRejectsSingleBodyBeyondRetention(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	_, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "too-large", Payload: []byte("preview"), BodyRef: "body-ref", FullBody: []byte("0123456789")}, SessionJournalRetention{Count: 4, Bytes: 8})
	if !errors.Is(err, ErrSessionJournalEntryTooLarge) {
		t.Fatalf("err=%v, want ErrSessionJournalEntryTooLarge", err)
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || len(window.Entries) != 0 {
		t.Fatalf("window=%+v err=%v, want no entries after rejected body", window, err)
	}
}

func TestSessionJournalPageReportsGapAfterRetentionPrunesHistory(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 10, Bytes: 1024}
	for index := 1; index <= 11; index++ {
		key := fmt.Sprintf("item-%d", index)
		if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(key)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	recent, err := r.ReadSessionJournalPage("session", 0, 4)
	if err != nil || recent.NextBefore != 8 || len(recent.Entries) != 4 {
		t.Fatalf("recent=%+v err=%v", recent, err)
	}
	older, err := r.ReadSessionJournalPage("session", recent.NextBefore, 4)
	if err != nil || older.NextBefore != 4 || len(older.Entries) != 4 {
		t.Fatalf("older=%+v err=%v", older, err)
	}
	boundary, err := r.ReadSessionJournalPage("session", older.NextBefore, 4)
	if err != nil || boundary.NextBefore != 2 || len(boundary.Entries) != 2 {
		t.Fatalf("boundary=%+v err=%v", boundary, err)
	}
	var gap *SessionJournalHistoryGapError
	_, err = r.ReadSessionJournalPage("session", boundary.NextBefore, 4)
	if !errors.As(err, &gap) {
		t.Fatalf("err=%v, want SessionJournalHistoryGapError", err)
	}
	if gap.EarliestSeq != 2 || gap.LatestSeq != 11 {
		t.Fatalf("gap=%+v, want earliest=2 latest=11", gap)
	}
}

func TestSessionJournalPageExposesProviderHistoryBoundaryUntilPruned(t *testing.T) {
	reg := openTestRegistry(t)
	register(t, reg, "s")
	retention := SessionJournalRetention{Count: 2, Bytes: 4096}
	if err := reg.RecordSessionJournalHistoryBoundary("s", 9); err != nil {
		t.Fatal(err)
	}
	if _, _, err := reg.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "s", Kind: "text", ProviderKey: "a", Payload: []byte(`{"itemId":"a"}`)}, retention); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordSessionJournalHistoryBoundary("s", 3); err != nil {
		t.Fatal(err)
	}
	page, err := reg.ReadSessionJournalPage("s", 0, 10)
	if err != nil || page.HistoryBefore != 9 || page.NextBefore != 0 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	for _, key := range []string{"b", "c"} {
		if _, _, err := reg.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "s", Kind: "text", ProviderKey: key, Payload: []byte(`{"itemId":"` + key + `"}`)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	page, err = reg.ReadSessionJournalPage("s", 0, 10)
	if err != nil || page.HistoryBefore != 0 {
		t.Fatalf("pruned journal still exposed provider boundary: %+v err=%v", page, err)
	}
}

func TestSessionJournalWithoutRecordedBoundaryEndsInHistoryGap(t *testing.T) {
	reg := openTestRegistry(t)
	register(t, reg, "s")
	retention := SessionJournalRetention{Count: 8, Bytes: 4096}
	for _, key := range []string{"a", "b"} {
		if _, _, err := reg.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "s", Kind: "text", ProviderKey: key, Payload: []byte(`{"itemId":"` + key + `"}`)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	if err := reg.RecordSessionJournalHistoryBoundary("s", 5); err != nil {
		t.Fatal(err)
	}
	page, err := reg.ReadSessionJournalPage("s", 0, 10)
	if err != nil || len(page.Entries) != 2 || page.HistoryBefore != 0 || page.NextBefore != page.Entries[0].Seq {
		t.Fatalf("journal with unknown older boundary reported exhaustion: %+v err=%v", page, err)
	}
	_, err = reg.ReadSessionJournalPage("s", page.NextBefore, 10)
	var gap *SessionJournalHistoryGapError
	if !errors.As(err, &gap) || gap.EarliestSeq != page.Entries[0].Seq {
		t.Fatalf("older read past unknown boundary err=%v", err)
	}
}

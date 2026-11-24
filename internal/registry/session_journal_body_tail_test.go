package registry

import (
	"errors"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestATailThatDoesNotStartWhereTheStoredBodyEndsIsRejected(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "tail", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}
	if err := r.RecordObservedSession(session); err != nil {
		t.Fatal(err)
	}
	retention := SessionJournalRetention{Count: 100, Bytes: 1 << 20}
	stored := "draft"
	entry := SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "reply", Payload: []byte(`{"role":"assistant"}`), BodyRef: "ref-1", FullBody: []byte(stored)}
	if _, _, err := r.AppendSessionJournalEntry(entry, retention); err != nil {
		t.Fatal(err)
	}
	other := SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "other", Payload: []byte(`{"role":"assistant"}`), BodyRef: "ref-2", FullBody: []byte("notes")}
	if _, _, err := r.AppendSessionJournalEntry(other, retention); err != nil {
		t.Fatal(err)
	}
	before, err := r.SessionJournalAfter(session.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	tail := func(ref string, from int) error {
		_, err := r.AppendSessionJournalBody(SessionJournalEntry{SessionID: session.ID, Kind: "text", ProviderKey: "reply", Payload: []byte(`{"role":"assistant"}`), BodyRef: ref}, from, []byte(" continued"), retention)
		return err
	}
	for _, tc := range []struct {
		name string
		ref  string
		from int
	}{
		{name: "the tail starts before the stored end", ref: "ref-1", from: len(stored) - 1},
		{name: "the tail starts past the stored end", ref: "ref-1", from: len(stored) + len(" rejected")},
		{name: "the tail names another item's body", ref: "ref-2", from: len(stored)},
	} {
		if err := tail(tc.ref, tc.from); !errors.Is(err, ErrSessionJournalBodyMoved) {
			t.Fatalf("%s: err=%v, want ErrSessionJournalBodyMoved", tc.name, err)
		}
		body, _, err := r.SessionJournalBody(session.ID, "ref-1", 0, 1<<10)
		if err != nil || string(body) != stored {
			t.Fatalf("%s: stored body=%q err=%v, want %q", tc.name, body, err, stored)
		}
		if body, _, err := r.SessionJournalBody(session.ID, "ref-2", 0, 1<<10); err != nil || string(body) != "notes" {
			t.Fatalf("%s: the other item's body=%q err=%v, want %q", tc.name, body, err, "notes")
		}
		after, err := r.SessionJournalAfter(session.ID, 0, 10)
		if err != nil || after.LatestSeq != before.LatestSeq {
			t.Fatalf("%s: latest seq %d (err %v), want %d unchanged", tc.name, after.LatestSeq, err, before.LatestSeq)
		}
	}
	if err := tail("ref-1", len(stored)); err != nil {
		t.Fatal(err)
	}
	body, _, err := r.SessionJournalBody(session.ID, "ref-1", 0, 1<<10)
	if err != nil || string(body) != stored+" continued" {
		t.Fatalf("stored body=%q err=%v", body, err)
	}
}

package registry

import (
	"bytes"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCatalogEventsReplayDeduplicateAndPersistEpoch(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.EnsureCatalogState(); err != nil {
		t.Fatal(err)
	}
	epoch, latest, err := r.CatalogState()
	if err != nil || epoch == "" || latest != 0 {
		t.Fatalf("epoch=%q latest=%d err=%v", epoch, latest, err)
	}
	first, created, err := r.AppendCatalogEvent(CatalogEvent{DedupeKey: "session:s1:1", Type: "session.upserted", EntityID: "s1", Payload: []byte(`{"id":"s1"}`)})
	if err != nil || !created || first.Seq != 1 {
		t.Fatalf("first=%+v created=%v err=%v", first, created, err)
	}
	again, created, err := r.AppendCatalogEvent(CatalogEvent{DedupeKey: "session:s1:1", Type: "session.upserted", EntityID: "s1", Payload: []byte(`ignored`)})
	if err != nil || created || again.Seq != first.Seq || !bytes.Equal(again.Payload, first.Payload) {
		t.Fatalf("again=%+v created=%v err=%v", again, created, err)
	}
	second, created, err := r.AppendCatalogEvent(CatalogEvent{DedupeKey: "session:s1:2", Type: "session.upserted", EntityID: "s1", Payload: []byte(`{"id":"s1","status":"busy"}`)})
	if err != nil || !created || second.Seq != 2 {
		t.Fatalf("second=%+v created=%v err=%v", second, created, err)
	}
	window, err := r.CatalogEventsAfter(first.Seq, 10)
	if err != nil || window.Gap || window.HostEpoch != epoch || len(window.Events) != 1 || window.Events[0].Seq != second.Seq {
		t.Fatalf("window=%+v err=%v", window, err)
	}
}

func TestAppendCatalogEventTxRollsBackWithOwnerTransaction(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.EnsureCatalogState(); err != nil {
		t.Fatal(err)
	}
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, created, err := r.AppendCatalogEventTx(tx, CatalogEvent{DedupeKey: "rollback", Type: "delivery.problem", EntityID: "intent-1", Payload: []byte(`{"id":"intent-1"}`)}); err != nil || !created {
		t.Fatalf("append created=%v err=%v", created, err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	window, err := r.CatalogEventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(window.Events) != 0 {
		t.Fatalf("events survived rollback: %+v", window.Events)
	}
}

func TestRecordCatalogSessionCommitsStateAndEventTogether(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "catalog-session", Surface: surface.KindCodex, Name: "Catalog"}
	event, created, err := r.RecordCatalogSession(CatalogSessionState{Session: session, HostProject: []byte(`{"id":"project-1"}`), Checkout: []byte(`{"id":"checkout-1"}`), ObservedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)}, CatalogEvent{DedupeKey: "catalog-session:1", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":{"id":"catalog-session"}}`)})
	if err != nil || !created || event.Seq != 1 {
		t.Fatalf("event=%+v created=%v err=%v", event, created, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CatalogSeq != event.Seq || len(snapshot.Sessions) != 1 || snapshot.Sessions[0].Session.ID != session.ID || string(snapshot.Sessions[0].HostProject) != `{"id":"project-1"}` {
		t.Fatalf("snapshot=%+v", snapshot)
	}
}

func TestSessionJournalSourceEpochChangesOnRestart(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	first, err := r.BeginSessionJournalSource("session")
	if err != nil || first == "" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	second, err := r.BeginSessionJournalSource("session")
	if err != nil || second == "" || second == first {
		t.Fatalf("second=%q first=%q err=%v", second, first, err)
	}
}

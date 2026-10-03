package registry

import (
	"bytes"
	"testing"
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

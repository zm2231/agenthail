package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func recordPresentationSession(t *testing.T, r *Registry, id, fingerprint string) {
	t.Helper()
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}, Project: json.RawMessage(`{}`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: fingerprint}, CatalogEvent{DedupeKey: id + fingerprint, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogPresentationReadsFingerprintsOnlyForStaleRows(t *testing.T) {
	r := openTestRegistry(t)
	for _, id := range []string{"a", "b"} {
		recordPresentationSession(t, r, id, `{"id":"`+id+`","plan":{"large":"`+strings.Repeat("x", 10000)+`"}}`)
	}
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "a", TargetSessionID: "b", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FailDeliveryIntent(intent.ID, "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveSession("b", time.Now()); err != nil {
		t.Fatal(err)
	}
	_, seq, _ := r.CatalogState()
	generations := map[string]uint64{}
	read, err := r.ReadCatalogPresentation(context.Background(), func(id string, generation uint64) bool {
		generations[id] = generation
		return id == "a"
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Rows) != 2 || read.CatalogSeq != seq || read.Outgoing["a"] != 1 || read.RefreshAt.IsZero() {
		t.Fatalf("read=%+v", read)
	}
	if len(read.Fingerprints) != 1 || !strings.Contains(string(read.Fingerprints["a"]), "large") {
		t.Fatalf("fingerprints=%v", read.Fingerprints)
	}
	for _, row := range read.Rows {
		if row.Freshness.Generation == 0 || row.Freshness.Generation != generations[row.ID] {
			t.Fatalf("row %s generation %d, asked with %d", row.ID, row.Freshness.Generation, generations[row.ID])
		}
		if row.Archived != (row.ID == "b") || row.Name != row.ID || row.Status != string(surface.StatusIdle) {
			t.Fatalf("row=%+v", row)
		}
	}
}

func TestCatalogPresentationGenerationChangesWithEveryFingerprintWrite(t *testing.T) {
	r := openTestRegistry(t)
	recordPresentationSession(t, r, "a", `{"capabilities":{"send":false}}`)
	first := presentationGeneration(t, r, "a")
	recordPresentationSession(t, r, "a", `{"capabilities":{"send":true}}`)
	if second := presentationGeneration(t, r, "a"); second <= first {
		t.Fatalf("a fingerprint-only change kept generation %d (was %d)", second, first)
	}
}

func presentationGeneration(t *testing.T, r *Registry, id string) uint64 {
	t.Helper()
	read, err := r.ReadCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range read.Rows {
		if row.ID == id {
			return row.Freshness.Generation
		}
	}
	t.Fatalf("no row %s", id)
	return 0
}

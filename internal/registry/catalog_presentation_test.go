package registry

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCatalogPresentationHydratesOnlySelectedRowsFromOneRead(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now().UTC()
	for _, id := range []string{"a", "b"} {
		raw := `{"id":"` + id + `","plan":{"large":"` + strings.Repeat("x", 10000) + `"}}`
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}, Project: json.RawMessage(`{}`), Checkout: json.RawMessage(`{}`), ObservedAt: now, ProjectionFingerprint: raw}, CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	intent, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: "a", TargetSessionID: "b", Status: DeliveryIntentQueued, Evidence: surface.EvidenceQueued})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.FailDeliveryIntent(intent.ID, "synthetic failure"); err != nil {
		t.Fatal(err)
	}
	_, seq, _ := r.CatalogState()
	full, err := r.ReadCatalogPresentation(context.Background(), func(read CatalogPresentation) ([]string, error) {
		if len(read.Rows) != 2 || read.CatalogSeq != seq || read.Outgoing["a"] != 1 || read.RefreshAt.IsZero() {
			t.Fatalf("read=%+v", read)
		}
		for _, row := range read.Rows {
			if strings.Contains(string(row.Data), "large") {
				t.Fatal("full plan decoded during scan")
			}
		}
		if err := r.ArchiveSession("b", now); err != nil {
			t.Fatal(err)
		}
		return []string{"a"}, nil
	})
	if err != nil || len(full) != 1 || !strings.Contains(string(full["a"]), "large") {
		t.Fatalf("full=%v err=%v", full, err)
	}
	_, err = r.ReadCatalogPresentation(context.Background(), func(read CatalogPresentation) ([]string, error) {
		for _, row := range read.Rows {
			if row.ID == "b" && !strings.Contains(string(row.Data), `"archived":true`) {
				t.Fatalf("archive missing: %s", row.Data)
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCatalogPresentationDoesNotResurrectDeletedLocalAliases(t *testing.T) {
	r := openTestRegistry(t)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, Project: json.RawMessage(`{}`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"alias":"old-alias"}`}, CatalogEvent{DedupeKey: "a", Type: "session.upserted", EntityID: "a", Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("old-alias", "a"); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveAlias("old-alias"); err != nil {
		t.Fatal(err)
	}
	_, err := r.ReadCatalogPresentation(context.Background(), func(read CatalogPresentation) ([]string, error) {
		var row struct {
			Alias string `json:"alias"`
		}
		if err := json.Unmarshal(read.Rows[0].Data, &row); err != nil {
			t.Fatal(err)
		}
		if row.Alias != "" {
			t.Fatalf("deleted alias reappeared: %s", row.Alias)
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

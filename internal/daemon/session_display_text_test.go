package daemon

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestDisplayCountsUseSingularForOne(t *testing.T) {
	if got := sessionDisplay(dashboardSession{QueueCount: 1}, time.Now()).Detail; got != "1 message waiting" {
		t.Fatalf("detail=%q", got)
	}
	if got := sessionDisplay(dashboardSession{QueueCount: 3}, time.Now()).Detail; got != "3 messages waiting" {
		t.Fatalf("detail=%q", got)
	}
}

func TestQuietDetailDoesNotRepeatTheInstantLabel(t *testing.T) {
	now := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	row := dashboardSession{Status: surface.StatusBusy, Activity: &surface.Activity{LastProgressAt: now.Add(-11 * time.Minute)}}
	got := sessionDisplay(row, now)
	if got.Detail != "" || got.QuietSince == nil {
		t.Fatalf("display=%+v", got)
	}
}

func TestRevisionRowsFollowStalenessAndGeneration(t *testing.T) {
	record := func() registry.CatalogPresentationRow {
		return registry.CatalogPresentationRow{ID: "s", Freshness: registry.CatalogPresentationFreshness{Generation: 4}}
	}
	a, _ := json.Marshal(newRevisionRow(record(), dashboardSession{}))
	stale := record()
	stale.Freshness.Stale = true
	c, _ := json.Marshal(newRevisionRow(stale, dashboardSession{}))
	if string(a) == string(c) {
		t.Fatal("staleness must change the revision input")
	}
	moved := record()
	moved.Freshness.Generation = 5
	d, _ := json.Marshal(newRevisionRow(moved, dashboardSession{}))
	if string(a) == string(d) {
		t.Fatal("a new projection generation must change the revision input")
	}
}

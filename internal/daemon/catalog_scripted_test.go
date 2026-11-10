package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestALiveScriptedRunAgenthailDidNotStartIsKeptButNotListed(t *testing.T) {
	d, r, adapter := endedFixture(t)
	if err := r.RegisterSession(claudeRow("agent-run")); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordSessionOrigin(registry.OriginOf("agent-run", "")); err != nil {
		t.Fatal(err)
	}
	listedBefore := claudeRow("listed-before")
	adapter.sessions[listedBefore.ID] = listedBefore
	d.discoverCatalog(context.Background())
	if !catalogHasRow(t, r, listedBefore.ID) {
		t.Fatal("setup: the run had no row before its entrypoint was known")
	}
	for id, entrypoint := range map[string]string{"scripted": "sdk-cli", "agent-run": "sdk-cli", "hosted": "sdk-ts", "listed-before": "sdk-py"} {
		session := claudeRow(id)
		session.Status, session.Entrypoint = surface.StatusBusy, entrypoint
		adapter.sessions[id] = session
	}
	d.discoverCatalog(context.Background())
	for id, listed := range map[string]bool{"scripted": false, "listed-before": false, "agent-run": true, "hosted": true} {
		if catalogHasRow(t, r, id) != listed {
			t.Errorf("%s listed=%v, want %v", id, !listed, listed)
		}
	}
	if stored, err := r.Session("scripted"); err != nil || stored.ID != "scripted" {
		t.Fatalf("a hidden run must stay registered: %v", err)
	}
	if hidden, err := r.HiddenCatalogRows(string(surface.KindClaude)); err != nil || len(hidden) != 0 {
		t.Fatalf("hidden rows left in the catalog: %v %v", hidden, err)
	}
}

func TestScriptedRowsListedBeforeAreRemovedOnceTheirTranscriptsAreRead(t *testing.T) {
	d, r, adapter := endedFixture(t)
	for _, id := range []string{"scripted-a", "scripted-b", "terminal"} {
		adapter.sessions[id] = claudeRow(id)
	}
	d.discoverCatalog(context.Background())
	for id := range adapter.sessions {
		delete(adapter.sessions, id)
	}
	d.discoverCatalog(context.Background())
	for _, id := range []string{"scripted-a", "scripted-b", "terminal"} {
		if !viewHas(t, d, "all", id) {
			t.Fatalf("setup: %s is not in All", id)
		}
	}
	adapter.entrypoints = map[string]string{"/fixture/scripted-a.jsonl": "sdk-cli", "/fixture/scripted-b.jsonl": "sdk-cli", "/fixture/terminal.jsonl": "cli"}
	d.catalogEnded.rechecked[surface.KindClaude] = time.Now().Add(-catalogEndedRecheck)
	d.discoverCatalog(context.Background())
	d.discoverCatalog(context.Background())
	for id, listed := range map[string]bool{"scripted-a": false, "scripted-b": false, "terminal": true} {
		if viewHas(t, d, "all", id) != listed {
			t.Errorf("%s in All=%v, want %v", id, !listed, listed)
		}
	}
	if _, err := r.Session("scripted-a"); err != nil {
		t.Fatalf("a removed run's session row went too: %v", err)
	}
	d.catalogEnded.rechecked[surface.KindClaude] = time.Now().Add(-catalogEndedRecheck)
	d.discoverCatalog(context.Background())
	if catalogHasRow(t, r, "scripted-a") {
		t.Fatal("the restore brought a hidden run back")
	}
}

func TestHiddenRowsLeaveInBatches(t *testing.T) {
	d, r, _ := endedFixture(t)
	ids := []string{}
	for index := range catalogHideBatch + 2 {
		session := claudeRow("batch-" + time.Duration(index).String())
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: session.ID}, registry.CatalogEvent{DedupeKey: "fixture:" + session.ID, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, session.ID)
	}
	entrypoints := map[string]string{}
	for _, id := range ids {
		entrypoints[id] = "sdk-cli"
	}
	if err := r.RecordEntrypoints(entrypoints); err != nil {
		t.Fatal(err)
	}
	if err := d.queueHiddenRemovals(surface.KindClaude); err != nil {
		t.Fatal(err)
	}
	if err := d.recordEndedDiscovery(surface.KindClaude, &endedSurface{}, map[string]struct{}{}, map[string]struct{}{}, time.Now()); err != nil {
		t.Fatal(err)
	}
	left, err := r.HiddenCatalogRows(string(surface.KindClaude))
	if err != nil || len(left) != 2 {
		t.Fatalf("after one pass %d hidden rows are left (%v), want 2", len(left), err)
	}
}

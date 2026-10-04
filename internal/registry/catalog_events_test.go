package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestCatalogDiscoveryFailureRetainsRowAndAdvancesStaleGeneration(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	projection := `{"id":"catalog-session","surface":"codex","name":"Catalog","cwd":"/work","status":"idle"}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "catalog-session", Surface: surface.KindCodex, Name: "Catalog", Cwd: "/work"}, HostProject: []byte(`{"id":"project"}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "catalog-session:initial", Type: "session.upserted", EntityID: "catalog-session", Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	events, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Minute))
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	row := snapshot.Sessions[0]
	if !row.Freshness.Stale || row.Freshness.Generation != 2 || !row.Freshness.ObservedAt.Equal(observed) || row.Session.ID != "catalog-session" || row.Session.Cwd != "/work" {
		t.Fatalf("row=%+v", row)
	}
	var payload struct {
		Session struct {
			Freshness CatalogFreshness `json:"freshness"`
		} `json:"session"`
	}
	if err := json.Unmarshal(events[0].Payload, &payload); err != nil || !payload.Session.Freshness.Stale || payload.Session.Freshness.Generation != 2 {
		t.Fatalf("payload=%s err=%v", events[0].Payload, err)
	}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: row.Session, HostProject: row.HostProject, Checkout: row.Checkout, ObservedAt: observed.Add(2 * time.Minute), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "catalog-session:recovery", Type: "session.upserted", EntityID: row.Session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	recovered, err := r.CatalogSnapshot()
	if err != nil || recovered.Sessions[0].Freshness.Stale || recovered.Sessions[0].Freshness.Generation != 3 {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
}

func TestFailedDiscoveryDoesNotAdvanceSuccessfulOmissionCount(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "omission-after-failure", Surface: surface.KindCodex}, HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: `{"id":"omission-after-failure","surface":"codex"}`}, CatalogEvent{DedupeKey: "omission-after-failure:initial", Type: "session.upserted", EntityID: "omission-after-failure", Payload: []byte(`{"session":{"id":"omission-after-failure"}}`)}); err != nil {
		t.Fatal(err)
	}
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Duration(attempt+1)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2); err != nil || len(events) != 0 {
		t.Fatalf("first successful omission events=%+v err=%v", events, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].Freshness.Stale || !snapshot.Sessions[0].Freshness.ObservedAt.Equal(observed) {
		t.Fatalf("after failure plus omission snapshot=%+v err=%v", snapshot, err)
	}
	if events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2); err != nil || len(events) != 1 {
		t.Fatalf("second consecutive omission events=%+v err=%v", events, err)
	}
}

func TestDiscoveryFailureBreaksSuccessfulOmissionRun(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "omission-run", Surface: surface.KindCodex}, HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: `{"id":"omission-run","surface":"codex"}`}, CatalogEvent{DedupeKey: "omission-run:initial", Type: "session.upserted", EntityID: "omission-run", Payload: []byte(`{"session":{"id":"omission-run"}}`)}); err != nil {
		t.Fatal(err)
	}
	if events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2); err != nil || len(events) != 0 {
		t.Fatalf("first omission events=%+v err=%v", events, err)
	}
	if _, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2); err != nil || len(events) != 0 {
		t.Fatalf("post-failure omission events=%+v err=%v", events, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].Freshness.Stale {
		t.Fatalf("row after broken omission run=%+v err=%v", snapshot, err)
	}
	if events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2); err != nil || len(events) != 1 {
		t.Fatalf("second post-failure omission events=%+v err=%v", events, err)
	}
}

func TestCatalogSnapshotPageFiltersAndBoundsRows(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	for i := 0; i < 40; i++ {
		id := fmt.Sprintf("catalog-%02d", i)
		project := "other"
		if i%2 == 0 {
			project = "target"
		}
		projection := fmt.Sprintf(`{"id":%q,"surface":"codex","name":%q,"cwd":"/work/%s","status":"idle"}`, id, "worker-"+fmt.Sprint(i), project)
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: id, Surface: surface.KindCodex, Name: "worker-" + fmt.Sprint(i), Cwd: "/work/" + project, LastActive: now.Add(-time.Duration(i) * time.Minute)}, HostProject: []byte(fmt.Sprintf(`{"id":%q}`, project)), Checkout: []byte(`{}`), ObservedAt: now, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{ProjectID: "target", Query: "worker-2", Limit: 1, Now: now})
	if err != nil || page.TotalMatching != 6 || len(page.Sessions) != 1 || page.Sessions[0].Session.ID != "catalog-02" || !page.HasMore {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	page, err = r.CatalogSnapshotPage(CatalogPageRequest{ProjectID: "target", Limit: 3, Now: now})
	if err != nil || page.TotalMatching != 20 || len(page.Sessions) != 3 || !page.HasMore {
		t.Fatalf("bounded page=%+v err=%v", page, err)
	}
	literal, err := r.CatalogSnapshotPage(CatalogPageRequest{Query: "worker-2%", Limit: 3, Now: now})
	if err != nil || literal.TotalMatching != 0 {
		t.Fatalf("wildcard query was not literal: page=%+v err=%v", literal, err)
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
	event, created, err := r.RecordCatalogSession(CatalogSessionState{Session: session, HostProject: []byte(`{"id":"project-1"}`), Checkout: []byte(`{"id":"checkout-1"}`), ObservedAt: time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC), ProjectionFingerprint: "initial"}, CatalogEvent{DedupeKey: "catalog-session:1", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":{"id":"catalog-session"}}`)})
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

func TestCatalogOmissionRequiresTwoSuccessfulReconciliations(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "omitted", Surface: surface.KindCodex}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, HostProject: []byte(`{"id":"project"}`), Checkout: []byte(`{"id":"checkout"}`), ProjectionFingerprint: "initial"}, CatalogEvent{DedupeKey: "omitted:upsert", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		events, err := r.ReconcileCatalogOmissions(surface.KindCodex, map[string]struct{}{}, 2)
		if err != nil {
			t.Fatal(err)
		}
		if len(events) != attempt-1 {
			t.Fatalf("attempt=%d events=%+v", attempt, events)
		}
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 0 {
		t.Fatalf("sessions=%+v", snapshot.Sessions)
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

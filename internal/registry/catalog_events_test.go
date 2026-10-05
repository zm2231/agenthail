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
	if !row.Freshness.Stale || row.Freshness.Generation != 2 || !row.Freshness.ObservedAt.Equal(observed) || row.Session.ID != "catalog-session" || row.Session.Cwd != "/work" || string(row.HostProject) != `{"id":"project"}` {
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

func TestDiscoveryFailureBreaksSuccessfulOmissionRun(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "omission-run", Surface: surface.KindCodex}, HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: `{"id":"omission-run","surface":"codex"}`}, CatalogEvent{DedupeKey: "omission-run:initial", Type: "session.upserted", EntityID: "omission-run", Payload: []byte(`{"session":{"id":"omission-run"}}`)}); err != nil {
		t.Fatal(err)
	}
	if events, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 2); err != nil || len(events) != 0 {
		t.Fatalf("first omission events=%+v err=%v", events, err)
	}
	if _, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if events, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 2); err != nil || len(events) != 0 {
		t.Fatalf("post-failure omission events=%+v err=%v", events, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].Freshness.Stale {
		t.Fatalf("row after broken omission run=%+v err=%v", snapshot, err)
	}
	if events, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 2); err != nil || len(events) != 1 {
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

func TestCatalogOmissionRequiresTwoSuccessfulReconciliations(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "omitted", Surface: surface.KindCodex}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, HostProject: []byte(`{"id":"project"}`), Checkout: []byte(`{"id":"checkout"}`), ProjectionFingerprint: "initial"}, CatalogEvent{DedupeKey: "omitted:upsert", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		events, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 2)
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

func TestRecordCatalogSurfacePublishesEveryHealthTransition(t *testing.T) {
	r := openTestRegistry(t)
	start := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for index, health := range []string{"healthy", "unavailable", "healthy", "healthy", "unavailable"} {
		observedAt := start.Add(time.Duration(index) * time.Second)
		if _, _, err := r.RecordCatalogSurface(CatalogSurfaceState{Surface: surface.KindCodex, Health: health, ObservedAt: observedAt}, CatalogEvent{DedupeKey: "surface.health:codex:" + health, Type: "surface.health", EntityID: "codex", Payload: []byte(`{"health":"` + health + `"}`)}); err != nil {
			t.Fatal(err)
		}
	}
	window, err := r.CatalogEventsAfter(0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, event := range window.Events {
		var payload struct {
			Health string `json:"health"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		got = append(got, payload.Health)
	}
	want := []string{"healthy", "unavailable", "healthy", "unavailable"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("surface health events=%v want=%v", got, want)
	}
}

func TestQueueMutationsAdvanceCatalogWatermarkWithPageMembership(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "queued-claude", Surface: surface.KindClaude, Name: "Queued", Status: surface.StatusIdle}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, HostProject: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"queued-claude","open":false}`}, CatalogEvent{DedupeKey: "session.upserted:queued-claude", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":{"id":"queued-claude"}}`)}); err != nil {
		t.Fatal(err)
	}
	page := func() CatalogPage {
		t.Helper()
		current, err := r.CatalogSnapshotPage(CatalogPageRequest{Scope: "current", Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		return current
	}
	empty := page()
	if len(empty.Sessions) != 0 {
		t.Fatalf("idle session listed as current: %+v", empty.Sessions)
	}
	if err := r.QueueMessage(session.ID, "first"); err != nil {
		t.Fatal(err)
	}
	queued := page()
	if queued.CatalogSeq <= empty.CatalogSeq || len(queued.Sessions) != 1 {
		t.Fatalf("enqueue seq=%d->%d sessions=%d", empty.CatalogSeq, queued.CatalogSeq, len(queued.Sessions))
	}
	item, err := r.ClaimNextMessage(session.ID, time.Now())
	if err != nil || item == nil {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
	claimed := page()
	if claimed.CatalogSeq != queued.CatalogSeq || len(claimed.Sessions) != 1 {
		t.Fatalf("claim changed membership or watermark: seq=%d->%d sessions=%d", queued.CatalogSeq, claimed.CatalogSeq, len(claimed.Sessions))
	}
	if err := r.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	drained := page()
	if drained.CatalogSeq <= claimed.CatalogSeq || len(drained.Sessions) != 0 {
		t.Fatalf("drain seq=%d->%d sessions=%d", claimed.CatalogSeq, drained.CatalogSeq, len(drained.Sessions))
	}
	if err := r.QueueMessage(session.ID, "expires"); err != nil {
		t.Fatal(err)
	}
	beforeExpiry := page()
	if expired, err := r.ExpireMessages(time.Now().Add(365 * 24 * time.Hour)); err != nil || expired != 1 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	swept := page()
	if swept.CatalogSeq <= beforeExpiry.CatalogSeq || len(swept.Sessions) != 0 {
		t.Fatalf("expiry seq=%d->%d sessions=%d", beforeExpiry.CatalogSeq, swept.CatalogSeq, len(swept.Sessions))
	}
}

package registry

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestCatalogEventsReplayDeduplicateAndPersistEpoch(t *testing.T) {
	r := openTestRegistry(t)
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
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "catalog-session", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Catalog", Cwd: "/work"}, Project: []byte(`{"id":"project"}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "catalog-session:initial", Type: "session.upserted", EntityID: "catalog-session", Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
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
	if !row.Freshness.Stale || row.Freshness.Generation != 2 || !row.Freshness.ObservedAt.Equal(observed) || row.Session.ID != "catalog-session" || row.Session.Cwd != "/work" || string(row.Project) != `{"id":"project"}` {
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
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: row.Session, Project: row.Project, Checkout: row.Checkout, ObservedAt: observed.Add(2 * time.Minute), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "catalog-session:recovery", Type: "session.upserted", EntityID: row.Session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	recovered, err := r.CatalogSnapshot()
	if err != nil || recovered.Sessions[0].Freshness.Stale || recovered.Sessions[0].Freshness.Generation != 3 {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
}

func TestRepeatedDiscoveryFailurePublishesOnlyTheStaleTransition(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	projection := `{"id":"repeat","surface":"codex","name":"Repeat","cwd":"/work","status":"idle"}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "repeat", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "Repeat", Cwd: "/work"}, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "repeat:initial", Type: "session.upserted", EntityID: "repeat", Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	if events, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Minute)); err != nil || len(events) != 1 {
		t.Fatalf("first failure events=%+v err=%v", events, err)
	}
	_, latest, err := r.CatalogState()
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if events, err := r.MarkCatalogDiscoveryFailure(surface.KindCodex, "provider unavailable", observed.Add(time.Duration(i+2)*time.Minute)); err != nil || len(events) != 0 {
			t.Fatalf("repeat failure %d events=%+v err=%v", i, events, err)
		}
	}
	if _, after, err := r.CatalogState(); err != nil || after != latest {
		t.Fatalf("latest seq %d -> %d err=%v", latest, after, err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil || len(snapshot.Sessions) != 1 || !snapshot.Sessions[0].Freshness.Stale || snapshot.Sessions[0].Freshness.Generation != 2 {
		t.Fatalf("snapshot=%+v err=%v", snapshot, err)
	}
	var failures int
	if err := r.db.QueryRow(`SELECT discovery_failures FROM catalog_sessions WHERE session_id='repeat'`).Scan(&failures); err != nil || failures != 4 {
		t.Fatalf("failures=%d err=%v", failures, err)
	}
}

func TestCatalogEventRetentionKeepsNewestWindow(t *testing.T) {
	r := openTestRegistry(t)
	total := catalogEventRetention + 25
	for i := 1; i <= total; i++ {
		if _, _, err := r.AppendCatalogEvent(CatalogEvent{DedupeKey: fmt.Sprintf("retention:%d", i), Type: "session.upserted", EntityID: "s", Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	var count, earliest, latest int
	if err := r.db.QueryRow(`SELECT COUNT(*),MIN(seq),MAX(seq) FROM catalog_events`).Scan(&count, &earliest, &latest); err != nil {
		t.Fatal(err)
	}
	if count != catalogEventRetention || latest != total || earliest != total-catalogEventRetention+1 {
		t.Fatalf("count=%d earliest=%d latest=%d", count, earliest, latest)
	}
}

func TestDiscoveryFailureBreaksSuccessfulOmissionRun(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 4, 12, 0, 0, 0, time.UTC)
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: "omission-run", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: `{"id":"omission-run","surface":"codex"}`}, CatalogEvent{DedupeKey: "omission-run:initial", Type: "session.upserted", EntityID: "omission-run", Payload: []byte(`{"session":{"id":"omission-run"}}`)}); err != nil {
		t.Fatal(err)
	}
	events, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, time.Now(), true, 2)
	var retired struct {
		Session struct {
			Status  surface.SessionStatus `json:"status"`
			Current bool                  `json:"current"`
		} `json:"session"`
	}
	if err != nil || len(events) != 1 || json.Unmarshal(events[0].Payload, &retired) != nil || retired.Session.Status != surface.StatusOffline || retired.Session.Current {
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
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "worker-" + fmt.Sprint(i), Cwd: "/work/" + project, LastActive: now.Add(-time.Duration(i) * time.Minute)}, Project: []byte(fmt.Sprintf(`{"id":%q}`, project)), Checkout: []byte(`{}`), ObservedAt: now, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: id, Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
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
	session := surface.Session{ID: "omitted", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{"id":"project"}`), Checkout: []byte(`{"id":"checkout"}`), ProjectionFingerprint: "initial"}, CatalogEvent{DedupeKey: "omitted:upsert", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
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

func TestRetainCatalogSurfacesDropsUnregisteredSurfaces(t *testing.T) {
	r := openTestRegistry(t)
	for _, kind := range []surface.SurfaceKind{surface.KindClaude, surface.KindCodex, surface.KindNotion} {
		if _, _, err := r.RecordCatalogSurface(CatalogSurfaceState{Surface: kind, Health: "unavailable"}, CatalogEvent{DedupeKey: "surface.health:" + string(kind), Type: "surface.health", EntityID: string(kind), Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RetainCatalogSurfaces([]surface.SurfaceKind{surface.KindClaude, surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, record := range snapshot.Surfaces {
		got = append(got, string(record.Surface))
	}
	if fmt.Sprint(got) != "[claude codex]" {
		t.Fatalf("surfaces=%v", got)
	}
}

func TestQueueMutationsAdvanceCatalogWatermarkWithPageMembership(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "queued-claude", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "Queued", Status: surface.StatusIdle}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"queued-claude","open":false}`}, CatalogEvent{DedupeKey: "session.upserted:queued-claude", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":{"id":"queued-claude"}}`)}); err != nil {
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
	if err := r.QueueMessage(session.ID, "canceled later"); err != nil {
		t.Fatal(err)
	}
	beforeCancel := page()
	if count, err := r.CancelMessagesForSession(session.ID); err != nil || count != 1 {
		t.Fatalf("canceled=%d err=%v", count, err)
	}
	swept := page()
	if swept.CatalogSeq <= beforeCancel.CatalogSeq || len(swept.Sessions) != 0 {
		t.Fatalf("cancel seq=%d->%d sessions=%d", beforeCancel.CatalogSeq, swept.CatalogSeq, len(swept.Sessions))
	}
}

func TestCatalogRowKeepsBackgroundTasksUntilTheSessionGoesUnobserved(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 20, 11, 0, 0, 0, time.UTC)
	background := &surface.BackgroundTasks{Running: true, Since: observed, Source: surface.BackgroundSourceTranscript, Tasks: []surface.BackgroundTask{
		{ID: "b1", Kind: surface.BackgroundShell, Description: "go test ./...", Since: observed},
		{ID: "m1", Kind: surface.BackgroundMonitor, Description: "file watcher", Ambient: true, Since: observed},
	}}
	session := surface.Session{ID: "claude-bg", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, LastActive: observed, BackgroundTasks: background}
	projection := `{"id":"claude-bg","surface":"claude","status":"idle","backgroundTasks":{"running":true,"source":"transcript"}}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "claude-bg:initial", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	stored, err := r.Session(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !surface.SameBackground(stored.BackgroundTasks, background) {
		t.Fatalf("stored background=%+v", stored.BackgroundTasks)
	}
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Limit: 10, Now: observed})
	if err != nil || len(page.Sessions) != 1 || !surface.SameBackground(page.Sessions[0].Session.BackgroundTasks, background) {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, observed.Add(time.Hour), true, 2); err != nil {
		t.Fatal(err)
	}
	page, err = r.CatalogSnapshotPage(CatalogPageRequest{Limit: 10, Now: observed.Add(time.Hour)})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if retired := page.Sessions[0]; retired.Session.BackgroundTasks != nil || strings.Contains(retired.ProjectionFingerprint, "backgroundTasks") {
		t.Fatalf("offline session still runs background work: %+v", retired)
	}
}

func TestCatalogRowKeepsAUsageLimitUntilTheSessionGoesUnobserved(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 22, 17, 43, 40, 0, time.UTC)
	limit := &surface.UsageLimit{ResetsAt: time.Date(2026, 10, 22, 18, 20, 0, 0, time.UTC)}
	session := surface.Session{ID: "claude-limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, LastActive: observed, UsageLimit: limit}
	projection := `{"id":"claude-limited","surface":"claude","status":"idle","usageLimit":{"resetsAt":"2026-10-22T18:20:00Z"}}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "claude-limited:initial", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	stored, err := r.Session(session.ID)
	if err != nil || !surface.SameUsageLimit(stored.UsageLimit, limit) {
		t.Fatalf("stored limit=%+v err=%v", stored.UsageLimit, err)
	}
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Limit: 10, Now: observed})
	if err != nil || len(page.Sessions) != 1 || !surface.SameUsageLimit(page.Sessions[0].Session.UsageLimit, limit) {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, observed.Add(time.Hour), true, 2); err != nil {
		t.Fatal(err)
	}
	page, err = r.CatalogSnapshotPage(CatalogPageRequest{Limit: 10, Now: observed.Add(time.Hour)})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if retired := page.Sessions[0]; retired.Session.UsageLimit != nil || strings.Contains(retired.ProjectionFingerprint, "usageLimit") {
		t.Fatalf("an unobserved session is still limited: %+v", retired)
	}
}

func TestSessionMissingFromDiscoveryStopsBeingBusyOrCurrent(t *testing.T) {
	r := openTestRegistry(t)
	observed := time.Date(2026, 10, 7, 11, 0, 0, 0, time.UTC)
	session := surface.Session{ID: "busy-thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy, LastActive: observed, WaitingOn: &surface.Waiting{Kind: surface.WaitingQuestion, ID: "busy-thread:question"}}
	projection := `{"id":"busy-thread","surface":"codex","status":"busy","current":true,"currentReason":"working","waitingOn":{"kind":"question","id":"busy-thread:question"}}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: observed, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "busy-thread:initial", Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	count := func(scope string) int {
		t.Helper()
		page, err := r.CatalogSnapshotPage(CatalogPageRequest{Scope: scope, Limit: 10, Now: observed.Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return len(page.Sessions)
	}
	if count("current") != 1 || count("running") != 1 {
		t.Fatalf("observed busy session current=%d running=%d", count("current"), count("running"))
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindCodex, map[string]struct{}{}, observed.Add(time.Hour), true, 2); err != nil {
		t.Fatal(err)
	}
	if count("current") != 0 || count("running") != 0 {
		t.Fatalf("unobserved session current=%d running=%d", count("current"), count("running"))
	}
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Limit: 10, Now: observed.Add(time.Hour)})
	if err != nil || len(page.Sessions) != 1 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	if retired := page.Sessions[0]; retired.Session.WaitingOn != nil || strings.Contains(retired.ProjectionFingerprint, "waitingOn") {
		t.Fatalf("offline session still waiting: %+v", retired)
	}
	if err := r.QueueMessage(session.ID, "held"); err != nil {
		t.Fatal(err)
	}
	if count("current") != 1 {
		t.Fatal("unobserved session with a queued message left current")
	}
}

func TestCatalogSnapshotReadsSessionsInOrderWithoutSorting(t *testing.T) {
	r := openTestRegistry(t)
	rows, err := r.db.Query(`EXPLAIN QUERY PLAN ` + catalogSnapshotQuery)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(plan, "\n")
	if strings.Contains(joined, "TEMP B-TREE") || !strings.Contains(joined, "sessions_recent") {
		t.Fatalf("plan:\n%s", joined)
	}
}

// A paired host's row carries its session's usage limit as the host
// projected it.
func TestAHostCatalogRowKeepsItsUsageLimit(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.AddHost(PairedHost{ID: "h00000000000b", Name: "server", Endpoint: "https://server.example.ts.net:7412", Address: "100.64.0.2", Token: "ahd_secret"}); err != nil {
		t.Fatal(err)
	}
	projection := `{"id":"remote-limited","surface":"claude","usageLimit":{"resetsAt":"2026-10-22T18:20:00Z"},"waitingOn":{"kind":"approval","id":"q1","answerable":true}}`
	if _, _, err := r.ApplyHostCatalogRow("h00000000000b", HostCatalogRow{Session: surface.Session{ID: "remote-limited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, EntityID: "remote-limited", Projection: json.RawMessage(projection)}); err != nil {
		t.Fatal(err)
	}
	stored, err := r.Session("remote-limited")
	want := &surface.UsageLimit{ResetsAt: time.Date(2026, 10, 22, 18, 20, 0, 0, time.UTC)}
	if err != nil || !surface.SameUsageLimit(stored.UsageLimit, want) || stored.WaitingOn == nil || !stored.WaitingOn.Answerable {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

func TestCatalogActiveSessionsAreTheSnapshotRowsThatAreRecentlyActive(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now()
	for _, session := range []surface.Session{
		{ID: "busy-long-ago", Status: surface.StatusBusy, LastActive: now.Add(-72 * time.Hour)},
		{ID: "idle-recent", Status: surface.StatusIdle, LastActive: now.Add(-time.Hour)},
		{ID: "idle-long-ago", Status: surface.StatusIdle, LastActive: now.Add(-72 * time.Hour)},
		{ID: "offline-recent", Status: surface.StatusOffline, LastActive: now.Add(-time.Minute)},
		{ID: "never-active", Status: surface.StatusIdle},
	} {
		session.Surface, session.RuntimeKind, session.Cwd = surface.KindCodex, surface.RuntimeCodexDesktop, "/work"
		projection := `{"id":"` + session.ID + `"}`
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: now, ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: session.ID, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, row := range snapshot.Sessions {
		if surface.RecentlyActive(row.Session, now) {
			want = append(want, row.Session.ID)
		}
	}
	active, err := r.CatalogActiveSessions(now.Add(-surface.ActiveWindow))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, row := range active {
		got = append(got, row.Session.ID)
	}
	if len(snapshot.Sessions) != 5 || strings.Join(got, ",") != strings.Join(want, ",") || strings.Join(got, ",") != "idle-recent,busy-long-ago" {
		t.Fatalf("active sessions %v, snapshot rows recently active %v", got, want)
	}
}

func TestCatalogActiveSessionsBoundIsTheFirstStoredMillisecondAtOrAfterSince(t *testing.T) {
	r := openTestRegistry(t)
	active := time.UnixMilli(time.Now().UnixMilli())
	session := surface.Session{ID: "edge", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Cwd: "/work", Status: surface.StatusIdle, LastActive: active}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: active, ProjectionFingerprint: `{"id":"edge"}`}, CatalogEvent{DedupeKey: "edge", Type: "session.upserted", EntityID: "edge", Payload: []byte(`{"session":{"id":"edge"}}`)}); err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		since time.Time
		want  int
	}{{active, 1}, {active.Add(time.Microsecond), 0}, {active.Add(-time.Microsecond), 1}} {
		rows, err := r.CatalogActiveSessions(check.since)
		if err != nil || len(rows) != check.want {
			t.Fatalf("since %s: %d rows err=%v, want %d", check.since.Sub(active), len(rows), err, check.want)
		}
		if now := check.since.Add(surface.ActiveWindow); surface.RecentlyActive(session, now) != (check.want == 1) {
			t.Fatalf("since %s disagrees with RecentlyActive", check.since.Sub(active))
		}
	}
}

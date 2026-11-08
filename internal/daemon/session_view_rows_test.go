package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

// The single-statement merge patch the presentation read used before it cached projections.
const presentationOracleSQL = `SELECT s.id,
 json_patch(json_remove(COALESCE(NULLIF(cs.projection_fingerprint,''),'{}'),
 '$.holders','$.plan','$.runtime','$.claudeConfigRoot'),
 json_object('name',s.name,'surface',s.surface,'status',s.status,'cwd',s.cwd,
 'lastActive',CASE WHEN s.last_active_ms>0 THEN strftime('%Y-%m-%dT%H:%M:%fZ',s.last_active_ms/1000.0,'unixepoch') ELSE NULL END,
 'registeredAt',strftime('%Y-%m-%dT%H:%M:%SZ',s.registered_at),
 'runtimeKind',s.runtime_kind,'source',s.source,'transport',s.transport,
 'unavailableReason',cs.unavailable_reason,
 'subagent',CASE WHEN s.parent_session_id!='' THEN json_object('parentId',s.parent_session_id,'rootId',s.root_session_id,'depth',s.subagent_depth,'nickname',s.agent_nickname,'role',s.agent_role) ELSE NULL END,
 'waitingOn',json(COALESCE(NULLIF(CAST(cs.waiting_on AS TEXT),''),'null')),
 'backgroundTasks',json(COALESCE(NULLIF(CAST(cs.background_tasks AS TEXT),''),'null')),
 'usageLimit',json(COALESCE(NULLIF(CAST(cs.usage_limit AS TEXT),''),'null')),
 'alias',CASE WHEN s.host_id='' THEN COALESCE(a.name,'') ELSE COALESCE(a.name,json_extract(cs.projection_fingerprint,'$.alias'),'') END,
 'queueCount',cs.host_queue_count+COALESCE(q.n,0),
 'archived',json(CASE WHEN s.host_id!='' THEN CASE WHEN COALESCE(json_extract(cs.projection_fingerprint,'$.archived'),0)=1 THEN 'true' ELSE 'false' END
 ELSE CASE WHEN sa.session_id IS NOT NULL OR COALESCE(p.archived_at_ms,0)>0 THEN 'true' ELSE 'false' END END)))
 FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
 LEFT JOIN aliases a ON a.session_id=s.id
 LEFT JOIN (SELECT session_id,COUNT(*) n FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id) q ON q.session_id=s.id
 LEFT JOIN session_archive sa ON sa.session_id=s.id
 LEFT JOIN projects p ON p.id=json_extract(cs.host_project,'$.localId')`

func assertPresentationMatchesMergePatch(t *testing.T, store *registry.Registry) {
	t.Helper()
	rows, err := store.ReadDB().Query(presentationOracleSQL)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	want := map[string]string{}
	for rows.Next() {
		var id, raw string
		if err := rows.Scan(&id, &raw); err != nil {
			t.Fatal(err)
		}
		var row dashboardSession
		if err := json.Unmarshal([]byte(raw), &row); err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(row)
		want[id] = string(encoded)
	}
	var cache presentationFingerprints
	cache.reset("")
	read, err := store.ReadCatalogPresentation(context.Background(), cache.stale)
	if err != nil {
		t.Fatal(err)
	}
	if err := cache.adopt(read); err != nil {
		t.Fatal(err)
	}
	if len(read.Rows) != len(want) || len(want) == 0 {
		t.Fatalf("rows=%d want=%d", len(read.Rows), len(want))
	}
	for _, record := range read.Rows {
		row, err := presentationRow(record, cache.rows[record.ID], (&Daemon{}).prViews)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(row)
		if string(encoded) != want[record.ID] {
			t.Errorf("%s:\n got %s\nwant %s", record.ID, encoded, want[record.ID])
		}
	}
}

func recordPresentation(t *testing.T, store *registry.Registry, session surface.Session, projection dashboardSession) {
	t.Helper()
	raw, _ := json.Marshal(projection)
	if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: string(raw)}, registry.CatalogEvent{DedupeKey: session.ID + string(raw), Type: "session.upserted", EntityID: session.ID, Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestPresentationRowsMatchTheMergePatchOfTheCatalog(t *testing.T) {
	_, store, _, _, _ := daemonFixture(t)
	active := time.Date(2026, 11, 3, 12, 0, 0, 123000000, time.UTC)
	waiting := &surface.Waiting{ID: "q1", Answerable: true}
	recordPresentation(t, store, surface.Session{ID: "waiting", Name: "Asks", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy, LastActive: active, Cwd: "/work", WaitingOn: waiting},
		dashboardSession{Name: "old name", Cwd: "/old", WaitingOn: &surface.Waiting{ID: "stale", Kind: "approval", Since: active}, BackgroundTasks: &surface.BackgroundTasks{Running: true, Source: "host"}, Alias: "projected", Capabilities: surface.Capabilities{Send: true}, Plan: &surface.Plan{}})
	recordPresentation(t, store, surface.Session{ID: "plain", Name: "Plain", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle},
		dashboardSession{Name: "projected", Archived: true, Subagent: &surface.Subagent{ParentID: "x", Nickname: "kept"}, BackgroundTasks: &surface.BackgroundTasks{Running: true}})
	recordPresentation(t, store, surface.Session{ID: "child", Name: "Child", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Subagent: &surface.Subagent{ParentID: "plain", RootID: "plain", Depth: 1, Role: "explorer"}},
		dashboardSession{Subagent: &surface.Subagent{ParentID: "elsewhere", Nickname: "Godel"}})
	if err := store.SetAlias("asker", "waiting"); err != nil {
		t.Fatal(err)
	}
	if err := store.ArchiveSession("plain", time.Now()); err != nil {
		t.Fatal(err)
	}
	hostID := "h0123456789ab"
	if _, err := store.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	for _, mirrored := range []dashboardSession{
		{ID: "remote", Name: "Remote", Surface: surface.KindCodex, Status: surface.StatusIdle, Alias: "their-alias", Archived: true, UnavailableReason: "peer is offline"},
		{ID: "remote-plain", Name: "Remote plain", Surface: surface.KindClaude, Status: surface.StatusBusy},
	} {
		raw, _ := json.Marshal(mirrored)
		session := surface.Session{ID: mirrored.ID, Name: mirrored.Name, Surface: mirrored.Surface, RuntimeKind: surface.RuntimeCodexDesktop, Status: mirrored.Status}
		if _, _, err := store.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::" + mirrored.ID, Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now(), UnavailableReason: mirrored.UnavailableReason}); err != nil {
			t.Fatal(err)
		}
	}
	assertPresentationMatchesMergePatch(t, store)
}

func TestARowChangedOnlyInItsProjectionChangesTheRevisionOfAnotherPage(t *testing.T) {
	path, store := openPresentationRegistry(t)
	d := newTestDaemon(t, store, nil)
	for _, id := range []string{"a", "b"} {
		recordPresentation(t, store, surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}, dashboardSession{Capabilities: surface.Capabilities{Send: false}})
	}
	first, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 1})
	if err != nil || len(first.View.Families) != 1 {
		t.Fatalf("first=%+v err=%v", first.View, err)
	}
	other := "a"
	if first.View.Families[0].RootID == "a" {
		other = "b"
	}
	rewriteProjection(t, path, other, `{"capabilities":{"send":true}}`, "projection_generation+1")
	second, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if second.View.Families[0].RootID != first.View.Families[0].RootID {
		t.Fatalf("the first page moved: %v then %v", first.View.Families[0].RootID, second.View.Families[0].RootID)
	}
	if second.View.Revision == first.View.Revision {
		t.Fatal("a projection-only change to a row on another page kept the revision")
	}
}

func openPresentationRegistry(t *testing.T) (string, *registry.Registry) {
	t.Helper()
	path := registrytest.Path(t)
	store, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return path, store
}

func rewriteProjection(t *testing.T, path, id, projection string, generation string) {
	t.Helper()
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE catalog_sessions SET projection_fingerprint=?,projection_generation=`+generation+` WHERE session_id=?`, projection, id); err != nil {
		t.Fatal(err)
	}
}

func viewSession(t *testing.T, d *Daemon, id string) dashboardSession {
	t.Helper()
	state, err := d.readSessionView(context.Background(), sessionViewRequest{Scope: "all", Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range state.Sessions {
		if row.ID == id {
			return row
		}
	}
	t.Fatalf("no %s in %+v", id, state.Sessions)
	return dashboardSession{}
}

func TestAProjectionAMigrationRewroteIsSeenByTheFirstRead(t *testing.T) {
	path, store := openPresentationRegistry(t)
	recordPresentation(t, store, surface.Session{ID: "a", Name: "a", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}, dashboardSession{Capabilities: surface.Capabilities{Send: false}})
	if viewSession(t, newTestDaemon(t, store, nil), "a").Capabilities.Send {
		t.Fatal("setup")
	}
	rewriteProjection(t, path, "a", `{"capabilities":{"send":true}}`, "projection_generation")
	if !viewSession(t, newTestDaemon(t, store, nil), "a").Capabilities.Send {
		t.Fatal("a daemon's first read missed a projection rewritten without a new generation")
	}
}

func TestARowRemovedAndAddedAgainIsReadFresh(t *testing.T) {
	_, store := openPresentationRegistry(t)
	hostID := "h0123456789ab"
	if _, err := store.AddHost(registry.PairedHost{ID: hostID, Name: "peer", Endpoint: "https://peer.example", Address: "100.64.0.2", Token: "synthetic"}); err != nil {
		t.Fatal(err)
	}
	mirror := func(send bool) {
		raw, _ := json.Marshal(dashboardSession{ID: "s", Name: "s", Surface: surface.KindCodex, Status: surface.StatusBusy, Capabilities: surface.Capabilities{Send: send}})
		session := surface.Session{ID: "s", Name: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}
		if _, _, err := store.ApplyHostCatalogRow(hostID, registry.HostCatalogRow{Session: session, EntityID: hostID + "::s", Projection: raw, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	mirror(false)
	d := newTestDaemon(t, store, nil)
	before := viewSession(t, d, hostID+"::s")
	if before.Capabilities.Send {
		t.Fatal("setup")
	}
	internal := d.presentation.fingerprints
	var stored string
	for id := range internal.rows {
		stored = id
	}
	if _, removed, err := store.RemoveHostCatalogRow(hostID, stored, hostID+"::s", "gone"); err != nil || !removed {
		t.Fatalf("removed=%v err=%v", removed, err)
	}
	if read, err := store.ReadCatalogPresentation(context.Background(), func(string, uint64) bool { return false }); err != nil || len(read.Rows) != 0 {
		t.Fatalf("the row was not removed: %+v err=%v", read.Rows, err)
	}
	mirror(true)
	after := viewSession(t, d, hostID+"::s")
	if after.Freshness.Generation <= before.Freshness.Generation {
		t.Fatalf("the row came back at generation %d after %d", after.Freshness.Generation, before.Freshness.Generation)
	}
	if !after.Capabilities.Send {
		t.Fatal("a row removed and added again kept its cached projection")
	}
}

func TestTheProjectionCacheIsDroppedWhenGenerationsGoBack(t *testing.T) {
	path, store := openPresentationRegistry(t)
	for _, id := range []string{"a", "b"} {
		recordPresentation(t, store, surface.Session{ID: id, Name: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusBusy}, dashboardSession{Capabilities: surface.Capabilities{Send: false}})
	}
	d := newTestDaemon(t, store, nil)
	viewSession(t, d, "b")
	rewriteProjection(t, path, "a", `{"capabilities":{"send":false}}`, "projection_generation-1")
	rewriteProjection(t, path, "b", `{"capabilities":{"send":true}}`, "projection_generation")
	if !viewSession(t, d, "b").Capabilities.Send {
		t.Fatal("a registry whose generations went back kept serving cached projections")
	}
}

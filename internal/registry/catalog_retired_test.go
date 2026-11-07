package registry

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func recordEndedRow(t *testing.T, r *Registry, session surface.Session, projection string) {
	t.Helper()
	session.Surface, session.RuntimeKind = surface.KindClaude, surface.RuntimeClaudeCLI
	if session.Status == "" {
		session.Status = surface.StatusIdle
	}
	if session.Transcript == "" {
		session.Transcript = "/fixture/" + session.ID + ".jsonl"
	}
	if session.LastActive.IsZero() {
		session.LastActive = time.Now().Add(-48 * time.Hour)
	}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "fixture:" + session.ID + ":" + projection, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func endedPasses(t *testing.T, r *Registry, passes int, gone ...string) {
	t.Helper()
	goneSet := map[string]struct{}{}
	for _, id := range gone {
		goneSet[id] = struct{}{}
	}
	for range passes {
		if _, err := r.RecordEndedCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, time.Now(), 2, goneSet); err != nil {
			t.Fatal(err)
		}
	}
}

func hasCatalogRow(t *testing.T, r *Registry, id string) bool {
	t.Helper()
	_, found, err := r.CatalogProjectionFingerprint(id)
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func isRetired(t *testing.T, r *Registry, id string) bool {
	t.Helper()
	var retired bool
	if err := r.read.QueryRow(`SELECT retired FROM catalog_sessions WHERE session_id=?`, id).Scan(&retired); err != nil {
		t.Fatal(err)
	}
	return retired
}

func TestAnEndedConversationStaysUntilItsTranscriptIsGoneAndCostsNoFurtherWrites(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "ended"}, `{"id":"ended","current":false}`)
	endedPasses(t, r, 2)
	if !hasCatalogRow(t, r, "ended") || !isRetired(t, r, "ended") {
		t.Fatal("the ended session left the catalog or did not move into history")
	}
	if session, err := r.Session("ended"); err != nil || session.Status != surface.StatusOffline {
		t.Fatalf("session=%+v err=%v", session, err)
	}
	var seq, generation int64
	read := func() (int64, int64) {
		var s, g int64
		if err := r.read.QueryRow(`SELECT COALESCE(MAX(seq),0),(SELECT projection_generation FROM catalog_sessions WHERE session_id='ended') FROM catalog_events`).Scan(&s, &g); err != nil {
			t.Fatal(err)
		}
		return s, g
	}
	seq, generation = read()
	endedPasses(t, r, 5)
	if s, g := read(); s != seq || g != generation {
		t.Fatalf("a kept row wrote on later passes: seq %d->%d generation %d->%d", seq, s, generation, g)
	}
	endedPasses(t, r, 1, "ended")
	if hasCatalogRow(t, r, "ended") {
		t.Fatal("a conversation whose transcript is gone stayed")
	}
}

func TestARecentlyEndedRowStaysInTheCurrentReadsUntilItsRecentWindowPasses(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "recent", LastActive: time.Now()}, `{"id":"recent","current":false}`)
	endedPasses(t, r, 2)
	if isRetired(t, r, "recent") {
		t.Fatal("a session active today left the current reads, which Recent needs")
	}
	if _, err := r.RecordEndedCatalogDiscovery(surface.KindClaude, map[string]struct{}{}, time.Now().Add(25*time.Hour), 2, nil); err != nil {
		t.Fatal(err)
	}
	if !isRetired(t, r, "recent") {
		t.Fatal("a row past the recent window stayed in the current reads")
	}
}

func TestARetiredRowLeavesCurrentReadsButNotHistory(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "history"}, `{"id":"history","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "live"}, `{"id":"live","current":true}`)
	if _, err := r.RecordEndedCatalogDiscovery(surface.KindClaude, map[string]struct{}{"live": {}}, time.Now(), 2, nil); err != nil {
		t.Fatal(err)
	}
	endedPasses(t, r, 0)
	if _, err := r.RecordEndedCatalogDiscovery(surface.KindClaude, map[string]struct{}{"live": {}}, time.Now(), 2, nil); err != nil {
		t.Fatal(err)
	}
	ids := func(rows []CatalogSessionState) map[string]bool {
		found := map[string]bool{}
		for _, row := range rows {
			found[row.Session.ID] = true
		}
		return found
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(snapshot.Sessions); got["history"] || !got["live"] {
		t.Fatalf("snapshot=%v", got)
	}
	current, err := r.CatalogSnapshotPage(CatalogPageRequest{Scope: "current", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(current.Sessions); got["history"] {
		t.Fatalf("current page=%v", got)
	}
	all, err := r.CatalogSnapshotPage(CatalogPageRequest{Scope: "all", Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(all.Sessions); !got["history"] || !got["live"] {
		t.Fatalf("all page=%v", got)
	}
	listing, err := r.CatalogListingForEntities([]string{"history", "live"})
	if err != nil {
		t.Fatal(err)
	}
	if listing.CurrentEntity("history") || !listing.CurrentEntity("live") {
		t.Fatal("the stream listing would send a retired row to devices as current")
	}
}

func TestARetiredRowComesBackWhenItRunsOrBecomesCurrent(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "resumed"}, `{"id":"resumed","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "promoted"}, `{"id":"promoted","current":false}`)
	endedPasses(t, r, 2)
	if err := r.RegisterSession(surface.Session{ID: "resumed", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy}); err != nil {
		t.Fatal(err)
	}
	if isRetired(t, r, "resumed") {
		t.Fatal("a running session stayed in history")
	}
	prior, found, err := r.CatalogProjectionFingerprint("promoted")
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	if _, _, err := r.UpdateCatalogSessionProjection("promoted", prior, `{"id":"promoted","current":true,"currentReason":"lead"}`); err != nil {
		t.Fatal(err)
	}
	if isRetired(t, r, "promoted") {
		t.Fatal("a session that became current stayed in history")
	}
}

func TestPresenceReadsARetiredRowOnceSomethingAsksAboutIt(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "asked"}, `{"id":"asked","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "quiet"}, `{"id":"quiet","current":false}`)
	endedPasses(t, r, 2)
	known := func(string, int64) bool { return true }
	ids := func(read CatalogPresenceRead) map[string]bool {
		found := map[string]bool{}
		for _, row := range read.Rows {
			found[row.Session.ID] = true
		}
		return found
	}
	read, err := r.CatalogPresenceRows(nil, "", 0, known)
	if err != nil {
		t.Fatal(err)
	}
	if got := ids(read); got["asked"] || got["quiet"] {
		t.Fatalf("presence read history: %v", got)
	}
	if err := r.QueueMessage("asked", "wake up"); err != nil {
		t.Fatal(err)
	}
	if read, err = r.CatalogPresenceRows(nil, "", 0, known); err != nil {
		t.Fatal(err)
	}
	if got := ids(read); !got["asked"] || got["quiet"] {
		t.Fatalf("a queued message must reach presence: %v", got)
	}
	if read, err = r.CatalogPresenceRowsWithRetired(nil, "", 0, known); err != nil {
		t.Fatal(err)
	}
	if got := ids(read); !got["quiet"] {
		t.Fatalf("an archive change must reach history: %v", got)
	}
}

func TestTheCurrentListingAgreesWithTheFullOneOnEveryUnretiredRow(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "old-parent"}, `{"id":"old-parent","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "live-parent"}, `{"id":"live-parent","current":true}`)
	endedPasses(t, r, 0)
	child := func(id, parent string, status surface.SessionStatus) surface.Session {
		return surface.Session{ID: id, Status: status, Subagent: &surface.Subagent{ParentID: parent, RootID: parent}}
	}
	recordEndedRow(t, r, child("under-old", "old-parent", surface.StatusBusy), `{"id":"under-old","current":true}`)
	recordEndedRow(t, r, child("under-live", "live-parent", surface.StatusBusy), `{"id":"under-live","current":true}`)
	recordEndedRow(t, r, child("idle-under-live", "live-parent", surface.StatusIdle), `{"id":"idle-under-live","current":false}`)
	seen := map[string]struct{}{"live-parent": {}, "under-old": {}, "under-live": {}, "idle-under-live": {}}
	for range 2 {
		if _, err := r.RecordEndedCatalogDiscovery(surface.KindClaude, seen, time.Now(), 2, nil); err != nil {
			t.Fatal(err)
		}
	}
	if !isRetired(t, r, "old-parent") {
		t.Fatal("fixture: old-parent should be retired")
	}
	full, err := catalogListingTx(r.read)
	if err != nil {
		t.Fatal(err)
	}
	current, err := catalogCurrentListingTx(r.read)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"live-parent", "under-old", "under-live", "idle-under-live"} {
		if full.Current(id) != current.Current(id) {
			t.Errorf("%s: full %v current %v", id, full.Current(id), current.Current(id))
		}
		if full.Rollups[id] != current.Rollups[id] || full.live[id] != current.live[id] {
			t.Errorf("%s rollups: full %+v current %+v", id, full.Rollups[id], current.Rollups[id])
		}
	}
	if current.Current("under-old") {
		t.Error("a row beneath a retired parent counted as current")
	}
}

func TestADiscoveryFailureLeavesHistoryAlone(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "history"}, `{"id":"history","current":false}`)
	endedPasses(t, r, 2)
	events, err := r.MarkCatalogDiscoveryFailure(surface.KindClaude, "discovery failed", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 || !isRetired(t, r, "history") {
		t.Fatalf("events=%d retired=%v", len(events), isRetired(t, r, "history"))
	}
}

func TestAPeerGetsARetiredRowOnceAndItsIDOnEveryReconnect(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "history"}, `{"id":"history","current":false}`)
	endedPasses(t, r, 2)
	ids := func(rows []CatalogSessionState) map[string]bool {
		found := map[string]bool{}
		for _, row := range rows {
			found[row.Session.ID] = true
		}
		return found
	}
	first, retired, err := r.CatalogMirrorSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	if !ids(first.Sessions)["history"] || len(retired.IDs) != 1 {
		t.Fatalf("first=%v retired=%+v", ids(first.Sessions), retired)
	}
	again, retired, err := r.CatalogMirrorSnapshot(&retired.Clock)
	if err != nil {
		t.Fatal(err)
	}
	if ids(again.Sessions)["history"] || len(retired.IDs) != 1 {
		t.Fatalf("again=%v retired=%+v", ids(again.Sessions), retired)
	}
	earlier := retired.Clock - 1
	changed, _, err := r.CatalogMirrorSnapshot(&earlier)
	if err != nil {
		t.Fatal(err)
	}
	if !ids(changed.Sessions)["history"] {
		t.Fatal("a row written after the peer's clock must be sent again")
	}
}

func TestRetiredCountsFollowAnArchiveSwapAtTheSameInstant(t *testing.T) {
	r := openTestRegistry(t)
	for id, project := range map[string]string{"a": `{"id":"project-a"}`, "b": `{"id":"project-b"}`} {
		recordEndedRow(t, r, surface.Session{ID: id}, `{"id":"`+id+`","current":false}`)
		if _, err := r.db.Exec(`UPDATE catalog_sessions SET host_project=? WHERE session_id=?`, []byte(project), id); err != nil {
			t.Fatal(err)
		}
	}
	endedPasses(t, r, 2)
	at := time.UnixMilli(1_790_000_000_000)
	if err := r.ArchiveSession("a", at); err != nil {
		t.Fatal(err)
	}
	archivedProject := func() string {
		t.Helper()
		read, err := r.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return true })
		if err != nil {
			t.Fatal(err)
		}
		for _, count := range read.Retired {
			if count.Archived {
				return count.ProjectID
			}
		}
		return ""
	}
	if got := archivedProject(); got != "project-a" {
		t.Fatalf("archived=%q", got)
	}
	if err := r.UnarchiveSession("a"); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveSession("b", at); err != nil {
		t.Fatal(err)
	}
	if got := archivedProject(); got != "project-b" {
		t.Fatalf("the count kept the old archive state: archived=%q", got)
	}
}

func TestRetiredCountsFollowTheFirstArchiveOfAnImplicitProject(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "worker"}, `{"id":"worker","current":false}`)
	if _, err := r.db.Exec(`UPDATE catalog_sessions SET host_project=? WHERE session_id='worker'`, []byte(`{"id":"repo:x","localId":"p-implicit"}`)); err != nil {
		t.Fatal(err)
	}
	endedPasses(t, r, 2)
	archived := func() bool {
		t.Helper()
		read, err := r.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return true })
		if err != nil || len(read.Retired) != 1 {
			t.Fatalf("retired=%+v err=%v", read.Retired, err)
		}
		return read.Retired[0].Archived
	}
	if archived() {
		t.Fatal("fixture: nothing is archived yet")
	}
	if err := r.ArchiveProject(ProjectRecord{ID: "p-implicit", WorkspaceRoot: "/fixture", Title: "x"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if !archived() {
		t.Fatal("the count missed a project's first archive")
	}
}

func TestPresentationCountsRetiredRowsItDoesNotRead(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "history"}, `{"id":"history","current":false}`)
	recordEndedRow(t, r, surface.Session{ID: "other"}, `{"id":"other","current":false}`)
	endedPasses(t, r, 2)
	stale := func(string, uint64) bool { return true }
	total := func(read CatalogPresentation) int {
		sum := 0
		for _, count := range read.Retired {
			sum += count.Sessions
		}
		return sum
	}
	read, err := r.ReadCurrentCatalogPresentation(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(read.Rows) != 0 || total(read) != 2 {
		t.Fatalf("rows=%d retired=%+v", len(read.Rows), read.Retired)
	}
	endedPasses(t, r, 1, "other")
	if read, err = r.ReadCurrentCatalogPresentation(context.Background(), stale); err != nil {
		t.Fatal(err)
	}
	if total(read) != 1 {
		t.Fatalf("the count kept a removed row: %+v", read.Retired)
	}
	history, err := r.ReadCatalogPresentation(context.Background(), stale)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Rows) != 1 || !history.Rows[0].Retired {
		t.Fatalf("history=%+v", history.Rows)
	}
}

func TestTheRetiredCountsFollowARetiredSessionsStatus(t *testing.T) {
	r := openTestRegistry(t)
	recordEndedRow(t, r, surface.Session{ID: "ended"}, `{"id":"ended","current":false}`)
	endedPasses(t, r, 2)
	if !isRetired(t, r, "ended") {
		t.Fatal("the fixture row did not retire")
	}
	statuses := func() []surface.SessionStatus {
		read, err := r.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		var found []surface.SessionStatus
		for _, count := range read.Retired {
			found = append(found, count.Status)
		}
		return found
	}
	if got := statuses(); len(got) != 1 || got[0] != surface.StatusOffline {
		t.Fatalf("retired counts=%v", got)
	}
	session, err := r.Session("ended")
	if err != nil {
		t.Fatal(err)
	}
	session.Status = surface.StatusIdle
	if err := r.RegisterSession(*session); err != nil {
		t.Fatal(err)
	}
	if !isRetired(t, r, "ended") {
		t.Fatal("registering the session brought its row back")
	}
	if got := statuses(); len(got) != 1 || got[0] != surface.StatusIdle {
		t.Fatalf("retired counts after the status changed=%v", got)
	}
}

func TestTheRetiredCountsFollowAMovedProject(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "ended", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: "/fixture/ended.jsonl", LastActive: time.Now().Add(-48 * time.Hour)}
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{"localId":"key-a"}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"ended","current":false}`}, CatalogEvent{DedupeKey: "fixture:ended", Type: "session.upserted", EntityID: "ended", Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	endedPasses(t, r, 2)
	if !isRetired(t, r, "ended") {
		t.Fatal("the fixture row did not retire")
	}
	if _, err := r.ObserveRepositoryKey("/work/a", "key-a"); err != nil {
		t.Fatal(err)
	}
	if err := r.ArchiveProject(ProjectRecord{ID: "key-a", WorkspaceRoot: "/work/a"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	archived := func() []bool {
		read, err := r.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
		if err != nil {
			t.Fatal(err)
		}
		var found []bool
		for _, count := range read.Retired {
			found = append(found, count.Archived)
		}
		return found
	}
	if got := archived(); len(got) != 1 || !got[0] {
		t.Fatalf("retired counts before the move=%v", got)
	}
	if outcome, err := r.ObserveRepositoryKey("/work/a", "key-b"); err != nil || outcome.Refused != nil {
		t.Fatalf("move=%+v %v", outcome, err)
	}
	var moved int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM projects WHERE id='key-b'`).Scan(&moved); err != nil || moved != 1 {
		t.Fatalf("the archived project did not move: %d %v", moved, err)
	}
	fresh, err := OpenCurrent(r.Path())
	if err != nil {
		t.Fatal(err)
	}
	defer fresh.Close()
	stored, err := fresh.ReadCurrentCatalogPresentation(context.Background(), func(string, uint64) bool { return false })
	if err != nil {
		t.Fatal(err)
	}
	if got := archived(); len(stored.Retired) != 1 || stored.Retired[0].Archived || len(got) != 1 || got[0] {
		t.Fatalf("retired counts after the move=%v, stored=%+v", got, stored.Retired)
	}
}

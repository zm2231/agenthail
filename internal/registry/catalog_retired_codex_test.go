package registry

import (
	"context"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func codexThread(id, parent, root string, depth int, ended bool) surface.Session {
	session := surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusNotLoaded, Ended: ended, Source: "vscode", Transport: "desktop", LastActive: time.Now().Add(-48 * time.Hour)}
	if !ended {
		session.Status = surface.StatusIdle
	}
	if parent != "" {
		session.Source = "subAgent"
		session.Subagent = &surface.Subagent{ParentID: parent, RootID: root, Depth: depth}
	}
	return session
}

func recordCodexThread(t *testing.T, r *Registry, session surface.Session) {
	t.Helper()
	projection := `{"id":"` + session.ID + `","status":"` + string(session.Status) + `"}`
	if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: projection}, CatalogEvent{DedupeKey: "fixture:" + session.ID + ":" + projection, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
}

func codexSweep(t *testing.T, r *Registry, listed ...string) {
	t.Helper()
	seen := map[string]struct{}{}
	for _, id := range listed {
		seen[id] = struct{}{}
	}
	if _, err := r.RecordCatalogDiscovery(surface.KindCodex, seen, time.Now(), true, 2); err != nil {
		t.Fatal(err)
	}
}

func TestAnEndedCodexThreadRetiresWithItsSubagentsAndComesBackWithThemWhenLoaded(t *testing.T) {
	r := openTestRegistry(t)
	family := []surface.Session{
		codexThread("root", "", "", 0, true),
		codexThread("child", "root", "root", 1, true),
		codexThread("grandchild", "child", "root", 2, true),
	}
	for _, session := range family {
		recordCodexThread(t, r, session)
	}
	codexSweep(t, r, "root", "child", "grandchild")
	for _, session := range family {
		if !isRetired(t, r, session.ID) {
			t.Fatalf("%s did not retire with its ended family", session.ID)
		}
	}
	snapshot, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Sessions) != 0 {
		t.Fatalf("the current snapshot still reads %d retired rows", len(snapshot.Sessions))
	}

	loaded := family[0]
	loaded.Status, loaded.Ended = surface.StatusIdle, false
	if err := r.RegisterSession(loaded); err != nil {
		t.Fatal(err)
	}
	for _, session := range family {
		if isRetired(t, r, session.ID) {
			t.Fatalf("%s stayed retired after its root was loaded again", session.ID)
		}
	}
}

func TestASubagentStaysUnretiredWhileItsParentIsCurrent(t *testing.T) {
	r := openTestRegistry(t)
	recordCodexThread(t, r, codexThread("root", "", "", 0, true))
	recordCodexThread(t, r, codexThread("child", "root", "root", 1, true))
	if err := r.QueueMessage("root", "keep me current"); err != nil {
		t.Fatal(err)
	}
	codexSweep(t, r, "root", "child")
	if isRetired(t, r, "root") || isRetired(t, r, "child") {
		t.Fatal("a thread with a queued send, or its subagent, retired")
	}
}

func TestACodexThreadARuntimeHoldsDoesNotRetire(t *testing.T) {
	r := openTestRegistry(t)
	recordCodexThread(t, r, codexThread("held", "", "", 0, false))
	codexSweep(t, r, "held")
	if isRetired(t, r, "held") {
		t.Fatal("a thread a runtime holds retired")
	}
}

func TestARetiredCodexThreadKeepsItsStatusInTheRetiredCounts(t *testing.T) {
	r := openTestRegistry(t)
	recordCodexThread(t, r, codexThread("root", "", "", 0, true))
	codexSweep(t, r, "root")
	tx, err := r.read.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	counts, err := r.catalogRetiredCounts(context.Background(), tx)
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 1 || counts[0].Status != surface.StatusNotLoaded || counts[0].Sessions != 1 {
		t.Fatalf("counts=%+v", counts)
	}
}

func TestAnUpgradeCountsEverySessionThatWentOfflineAsEnded(t *testing.T) {
	r := openTestRegistry(t)
	for _, session := range []surface.Session{
		{ID: "gone", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusOffline},
		{ID: "live", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle},
	} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`UPDATE sessions SET ended=0`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version=67`); err != nil {
		t.Fatal(err)
	}
	if err := r.migrate(true); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]bool{"gone": true, "live": false} {
		if session, err := r.Session(id); err != nil || session.Ended != want {
			t.Fatalf("%s: ended=%v err=%v, want %v", id, session.Ended, err, want)
		}
	}
}

func TestARetiredSubagentMovedUnderAnotherParentComesBack(t *testing.T) {
	r := openTestRegistry(t)
	recordCodexThread(t, r, codexThread("root", "", "", 0, true))
	recordCodexThread(t, r, codexThread("child", "root", "root", 1, true))
	recordCodexThread(t, r, codexThread("other", "", "", 0, false))
	codexSweep(t, r, "root", "child", "other")
	if !isRetired(t, r, "child") || isRetired(t, r, "other") {
		t.Fatal("fixture: the child should retire under its ended root and the held thread should not")
	}
	if err := r.RegisterSession(codexThread("child", "other", "other", 1, true)); err != nil {
		t.Fatal(err)
	}
	if isRetired(t, r, "child") {
		t.Fatal("a subagent moved under an unretired parent stayed retired, so that parent's rollup misses it")
	}
}

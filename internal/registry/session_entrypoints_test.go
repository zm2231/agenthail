package registry

import (
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func scriptedFixture(t *testing.T) *Registry {
	t.Helper()
	r := openTestRegistry(t)
	for id, entrypoint := range map[string]string{"scripted": "sdk-cli", "python": "sdk-py", "ours": "sdk-cli", "hosted": "sdk-ts", "terminal": "cli", "unknown": ""} {
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "run " + id, Cwd: "/work", Status: surface.StatusOffline, Transcript: "/t/" + id + ".jsonl", HasLocal: true, LastActive: time.Now()}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		if _, _, err := r.RecordCatalogSession(CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: id}, CatalogEvent{DedupeKey: "fixture:" + id, Type: "session.upserted", EntityID: id, Payload: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
		if err := r.RecordEntrypoints(map[string]string{id: entrypoint}); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordSessionOrigin(OriginOf("ours", "")); err != nil {
		t.Fatal(err)
	}
	return r
}

func TestOnlyScriptedRunsAgenthailDidNotStartAreHidden(t *testing.T) {
	r := scriptedFixture(t)
	hidden, err := r.HiddenCatalogRows(string(surface.KindClaude))
	if err != nil || !slices.Equal(hidden, []string{"python", "scripted"}) {
		t.Fatalf("hidden=%v err=%v, want the sdk-cli and sdk-py runs without an origin", hidden, err)
	}
	for id, entrypoint := range map[string]string{"scripted": "sdk-cli", "ours": "sdk-cli", "hosted": "sdk-ts", "new-run": "sdk-py", "new-terminal": "cli"} {
		got, err := r.HiddenScripted(id, entrypoint)
		if want := id == "scripted" || id == "new-run"; err != nil || got != want {
			t.Errorf("%s (%s): hidden=%v err=%v, want %v", id, entrypoint, got, err, want)
		}
	}
	found, err := r.SearchSessions(surface.KindClaude, "run", 20)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, session := range found {
		ids = append(ids, session.ID)
	}
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"hosted", "ours", "terminal", "unknown"}) {
		t.Fatalf("search found %v, want every session but the hidden scripted runs", ids)
	}
	if session, err := r.Session("scripted"); err != nil || session.ID != "scripted" {
		t.Fatalf("a hidden run must still be found by its id: %v", err)
	}
}

func TestRemovingHiddenRowsKeepsTheirSessionsAndSkipsOnesThatGainedAnOrigin(t *testing.T) {
	r := scriptedFixture(t)
	if err := r.RecordSessionOrigin(OriginOf("python", "")); err != nil {
		t.Fatal(err)
	}
	events, err := r.RemoveHiddenCatalogRows([]string{"scripted", "python", "terminal", "missing"})
	if err != nil || len(events) != 1 || events[0].EntityID != "scripted" || events[0].Type != "session.removed" {
		t.Fatalf("events=%+v err=%v, want one removal for the scripted run", events, err)
	}
	for id, want := range map[string]bool{"scripted": false, "python": true, "terminal": true} {
		if _, found, err := r.CatalogProjectionFingerprint(id); err != nil || found != want {
			t.Errorf("%s catalog row present=%v err=%v, want %v", id, found, err, want)
		}
	}
	if _, err := r.Session("scripted"); err != nil {
		t.Fatalf("the hidden run's session row was removed: %v", err)
	}
	if missing, err := r.EndedSessionsWithoutCatalogRow(string(surface.KindClaude)); err != nil || len(missing) != 0 {
		t.Fatalf("restore candidates=%+v err=%v, want none: a hidden run is never restored", missing, err)
	}
}

func TestAnEntrypointIsRecordedOnceAndNeverCleared(t *testing.T) {
	r := scriptedFixture(t)
	if err := r.RecordEntrypoints(map[string]string{"terminal": ""}); err != nil {
		t.Fatal(err)
	}
	rows, err := r.SessionsWithoutEntrypoint(string(surface.KindClaude))
	if err != nil || len(rows) != 1 || rows[0].ID != "unknown" {
		t.Fatalf("rows=%+v err=%v, want only the session never classified", rows, err)
	}
}

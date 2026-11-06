package registry

import (
	"testing"
	"time"
)

func pinnedIDs(t *testing.T, r *Registry) map[string]bool {
	t.Helper()
	pinned, err := r.PinnedSessions()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, p := range pinned {
		ids[p.Session.ID] = true
	}
	return ids
}

func TestAProjectsFirstLeadIsPinnedAndEachReplacementTakesTheLastOnesPin(t *testing.T) {
	r := coordinatorRegistry(t, "first", "second", "third", "fourth")
	spec := CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "work-lead", Surface: "claude", Cwd: "/work"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first"}); err != nil {
		t.Fatal(err)
	}
	if !pinnedIDs(t, r)["first"] {
		t.Fatal("a project's first lead was not pinned")
	}
	if _, err := r.HandOverCoordinator(spec, "first", "second", CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	if !pinnedIDs(t, r)["second"] {
		t.Fatal("the replacement of a pinned lead was not pinned")
	}
	if err := r.UnpinSession("second"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.HandOverCoordinator(spec, "second", "third", CoordinatorHandover{}); err != nil {
		t.Fatal(err)
	}
	if pinnedIDs(t, r)["third"] {
		t.Fatal("unpinning a lead did not survive its replacement")
	}
	if _, _, err := r.ReplaceCoordinator(CoordinatorSpec{ProjectID: "project-work", ProjectRoot: "/work", Name: "new-lead", Surface: "claude", Cwd: "/work"}, CoordinatorChange{SessionID: "fourth", Replace: true}); err != nil {
		t.Fatal(err)
	}
	if pinnedIDs(t, r)["fourth"] {
		t.Fatal("a lead taking over from an unpinned one was pinned by default")
	}
}

func TestOnlyThisHostsLiveUnstoppedPinnedSessionsAreKept(t *testing.T) {
	r := coordinatorRegistry(t, "kept", "archived", "stopped", "active-again", "mirrored")
	now := time.Now()
	for _, id := range []string{"kept", "archived", "stopped", "active-again", "mirrored"} {
		if _, err := r.PinSession(id, now); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.ArchiveSession("archived", now); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"stopped", "active-again"} {
		if err := r.RecordSessionStop(id, now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`UPDATE sessions SET last_active_ms=? WHERE id='active-again'`, now.Add(time.Second).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE sessions SET host_id='paired' WHERE id='mirrored'`); err != nil {
		t.Fatal(err)
	}
	got := pinnedIDs(t, r)
	if !got["kept"] || !got["active-again"] || got["archived"] || got["stopped"] || got["mirrored"] || len(got) != 2 {
		t.Fatalf("kept %v", got)
	}
}

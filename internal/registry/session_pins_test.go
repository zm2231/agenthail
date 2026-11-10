package registry

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func pinnedIDs(t *testing.T, r *Registry) map[string]bool {
	t.Helper()
	marks, err := r.SessionMarks()
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for id, mark := range marks {
		if !mark.PinnedAt.IsZero() {
			ids[id] = true
		}
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

func TestUpgradeKeepsPinsAndDropsTheKeepersState(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	old, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := old.RegisterSession(surface.Session{ID: "pinned", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}); err != nil {
		t.Fatal(err)
	}
	pinnedAt := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	if _, err := old.PinSession("pinned", pinnedAt); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`CREATE TABLE session_keep_alive (session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE, attempts INTEGER NOT NULL DEFAULT 0, failed_at_ms INTEGER NOT NULL DEFAULT 0, error TEXT NOT NULL DEFAULT '')`,
		`INSERT INTO session_keep_alive(session_id,attempts,failed_at_ms,error) VALUES('pinned',5,1,'gave up')`,
		`ALTER TABLE sessions ADD COLUMN stopped_at_ms INTEGER NOT NULL DEFAULT 0`,
		`UPDATE sessions SET stopped_at_ms=1 WHERE id='pinned'`,
		fmt.Sprintf(`PRAGMA user_version=%d`, schemaVersion-1),
	} {
		if _, err := old.db.Exec(statement); err != nil {
			old.Close()
			t.Fatal(err)
		}
	}
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	upgraded, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	if got, err := upgraded.SessionPinnedAt("pinned"); err != nil || !got.Equal(pinnedAt) {
		t.Fatalf("pin after upgrade = %v (%v), want %v", got, err, pinnedAt)
	}
	var tables, columns int
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name='session_keep_alive'`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if err := upgraded.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('sessions') WHERE name='stopped_at_ms'`).Scan(&columns); err != nil {
		t.Fatal(err)
	}
	if tables != 0 || columns != 0 {
		t.Fatalf("keeper state survived the upgrade: table=%d stopped_at_ms=%d", tables, columns)
	}
}

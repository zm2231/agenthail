package registry

import (
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARegistryAtSchema27RecordsWhoStartedASession(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []surface.Session{{ID: "planner", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, {ID: "worker", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}} {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`DROP TABLE session_origins`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version = 27`); err != nil {
		t.Fatal(err)
	}
	r.Close()

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version = %d err=%v, want %d", version, err, schemaVersion)
	}
	if err := r.RecordSessionOrigin(OriginOf("worker", "planner")); err != nil {
		t.Fatal(err)
	}
	origin, found, err := r.SessionOrigin("worker")
	if err != nil || !found || origin.Via != OriginAgent || origin.CreatorID != "planner" {
		t.Fatalf("origin = %+v found=%v err=%v, want started by the planner", origin, found, err)
	}
}

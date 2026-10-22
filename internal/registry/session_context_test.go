package registry

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestARegistryAtSchema28RecordsWhatASessionWasTold(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "s1", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`DROP TABLE session_context`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`PRAGMA user_version = 28`); err != nil {
		t.Fatal(err)
	}
	r.Close()

	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	now := time.Now()
	if claimed, err := r.ClaimSessionStart("s1", "startup:1", "a", now); err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v, want the first start claimed", claimed, err)
	}
	if change, err := r.ClaimContextChange("s1", "b", now); err != nil || change != ContextChanged {
		t.Fatalf("change=%v err=%v, want a change", change, err)
	}
}

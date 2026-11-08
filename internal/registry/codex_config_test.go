package registry

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestACodexThreadKeepsItsConfigOverrides(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "thread", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if empty, err := r.CodexConfigOverrides("thread"); err != nil || len(empty) != 0 {
		t.Fatalf("overrides=%v err=%v", empty, err)
	}
	previous, err := r.SetCodexConfigOverride("thread", "model_auto_compact_token_limit", 150000)
	if err != nil || len(previous) != 0 {
		t.Fatalf("previous=%v err=%v", previous, err)
	}
	if set, _ := r.CodexConfigOverrides("thread"); set["model_auto_compact_token_limit"] != float64(150000) {
		t.Fatalf("after setting: %v", set)
	}
	before, err := r.SetCodexConfigOverride("thread", "model_auto_compact_token_limit", nil)
	if err != nil {
		t.Fatal(err)
	}
	if cleared, _ := r.CodexConfigOverrides("thread"); len(cleared) != 0 {
		t.Fatalf("after clearing: %v", cleared)
	}
	if err := r.RestoreCodexConfigOverrides("thread", before); err != nil {
		t.Fatal(err)
	}
	if restored, _ := r.CodexConfigOverrides("thread"); restored["model_auto_compact_token_limit"] != float64(150000) {
		t.Fatalf("after restoring: %v", restored)
	}
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE id=?`, "thread"); err != nil {
		t.Fatal(err)
	}
	if gone, _ := r.CodexConfigOverrides("thread"); len(gone) != 0 {
		t.Fatalf("a removed thread keeps %v", gone)
	}
}

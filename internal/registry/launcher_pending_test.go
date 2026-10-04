package registry

import (
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestRegisterSessionAndDeletePendingKeepsIntentOnRegistrationFailure(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	pendingID, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "Build", "builder", surface.Location{Workspace: "workspace-1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE TRIGGER reject_launcher_session BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'registration blocked'); END`); err != nil {
		t.Fatal(err)
	}
	err = r.RegisterSessionAndDeletePending(surface.Session{ID: "created", Surface: surface.KindClaude}, pendingID, "builder")
	if err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != pendingID || pending[0].Alias != "builder" {
		t.Fatalf("pending=%+v", pending)
	}
}

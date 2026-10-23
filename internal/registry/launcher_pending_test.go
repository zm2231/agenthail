package registry

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestALaunchFoundAfterItsNameWasTakenIsRegisteredWithoutIt(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	launchedAt := time.Now()
	expires := launchedAt.Add(time.Minute)
	r.now = func() time.Time { return launchedAt }
	name, err := r.ReserveAlias("builder", expires)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "Build", name, surface.Location{Workspace: "workspace-1"}, launchedAt, expires); err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "owner", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}); err != nil {
		t.Fatal(err)
	}
	var reserved AliasReservedError
	if err := r.SetAlias("builder", "owner"); !errors.As(err, &reserved) {
		t.Fatalf("SetAlias while the launch holds the name: err=%v", err)
	}
	r.now = func() time.Time { return expires }
	if err := r.SetAlias("builder", "owner"); err != nil {
		t.Fatalf("SetAlias after the launch's name lapsed: %v", err)
	}
	launches, err := r.PendingLaunches()
	if err != nil || len(launches) != 1 {
		t.Fatalf("launches=%+v err=%v", launches, err)
	}
	named, err := r.RegisterSessionAndDeletePending(surface.Session{ID: "created", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, launches[0])
	if err != nil || named {
		t.Fatalf("named=%v err=%v", named, err)
	}
	if owner, err := r.LookupAlias("builder"); err != nil || owner != "owner" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
	if _, err := r.Session("created"); err != nil {
		t.Fatalf("the late launch is not registered: %v", err)
	}
	if launches, err := r.PendingLaunches(); err != nil || len(launches) != 0 {
		t.Fatalf("launches=%+v err=%v", launches, err)
	}
}

func TestRegisterSessionAndDeletePendingKeepsIntentOnRegistrationFailure(t *testing.T) {
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	name, err := r.ReserveAlias("builder", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	pendingID, err := r.RecordPendingLaunch(surface.LauncherCMUX, surface.KindClaude, "/repo", "Build", name, surface.Location{Workspace: "workspace-1"}, time.Now(), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE TRIGGER reject_launcher_session BEFORE INSERT ON sessions BEGIN SELECT RAISE(ABORT, 'registration blocked'); END`); err != nil {
		t.Fatal(err)
	}
	launches, err := r.PendingLaunches()
	if err != nil || len(launches) != 1 {
		t.Fatalf("launches=%+v err=%v", launches, err)
	}
	if _, err := r.RegisterSessionAndDeletePending(surface.Session{ID: "created", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, launches[0]); err == nil {
		t.Fatal("registration unexpectedly succeeded")
	}
	pending, err := r.PendingLaunches()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 || pending[0].ID != pendingID || pending[0].Alias.Name != "builder" {
		t.Fatalf("pending=%+v", pending)
	}
	var reserved AliasReservedError
	if _, err := r.ReserveAlias("builder", time.Now().Add(time.Hour)); !errors.As(err, &reserved) {
		t.Fatalf("the failed registration released the launch's name: err=%v", err)
	}
}

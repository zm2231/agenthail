package daemon

import (
	"slices"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

type boundedSurface struct {
	surface.Surface
	bounds surface.FolderBounds
}

func (b *boundedSurface) BoundFolders(bounds surface.FolderBounds) { b.bounds = bounds }

func TestASurfaceIsBoundedByTheRootsOfEachSessionsCeiling(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	for _, id := range []string{"worker", "person-started"} {
		if err := r.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle, Cwd: t.TempDir(), LastActive: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.WriteDB().Exec(`INSERT INTO session_ceilings(session_id,ceiling) VALUES('worker','{"permission":"full","roots":["/repo/.worktrees/task"]}')`); err != nil {
		t.Fatal(err)
	}
	claude := &boundedSurface{Surface: &daemonSurface{}}
	newTestDaemon(t, r, []surface.Surface{claude})
	if claude.bounds == nil {
		t.Fatal("the surface was not given the folders its sessions may work in")
	}
	if roots, err := claude.bounds("worker"); err != nil || !slices.Equal(roots, []string{"/repo/.worktrees/task"}) {
		t.Fatalf("worker roots=%v err=%v", roots, err)
	}
	if roots, err := claude.bounds("person-started"); err != nil || len(roots) != 0 {
		t.Fatalf("a session with no ceiling is bounded: roots=%v err=%v", roots, err)
	}
}

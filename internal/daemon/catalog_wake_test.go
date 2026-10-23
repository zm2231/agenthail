package daemon

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type watchingSurface struct {
	*daemonSurface
	dirs  func() []string
	mu    sync.Mutex
	added []surface.Session
	lists atomic.Int32
}

func newWatchingSurface(kind surface.SurfaceKind, dirs func() []string) *watchingSurface {
	return &watchingSurface{daemonSurface: &daemonSurface{kind: kind, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}}, dirs: dirs}
}

func (w *watchingSurface) CatalogWatchDirs() []string { return w.dirs() }

func (w *watchingSurface) List(context.Context) ([]surface.Session, error) {
	w.lists.Add(1)
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]surface.Session{}, w.added...), nil
}

func (w *watchingSurface) start(t *testing.T, id, folder string) {
	t.Helper()
	w.mu.Lock()
	w.added = append(w.added, surface.Session{ID: id, Surface: w.kind, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: folder, LastActive: time.Now().UTC()})
	w.mu.Unlock()
	if err := os.WriteFile(filepath.Join(folder, id+".json"), []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
}

func newestChild(dir string) string {
	entries, _ := os.ReadDir(dir)
	names := []string{}
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return filepath.Join(dir, names[len(names)-1])
}

func runCatalogLoop(t *testing.T, d *Daemon) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		d.runCatalog(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func awaitCatalogRow(t *testing.T, d *Daemon, id string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		snapshot, err := d.Registry.CatalogSnapshot()
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range snapshot.Sessions {
			if row.Session.ID == id {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s did not reach the catalog well before the next discovery pass", id)
}

func TestASessionStartedOutsideReachesTheCatalogWhenItsRecordAppears(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	records, rollouts := t.TempDir(), t.TempDir()
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	codex := newWatchingSurface(surface.KindCodex, func() []string { return []string{rollouts} })
	d.Surfaces = []surface.Surface{claude, codex}
	runCatalogLoop(t, d)
	awaitFederation(t, "the first discovery pass", func() bool { return claude.lists.Load() == 1 && codex.lists.Load() == 1 })

	claude.start(t, "outside", records)
	awaitCatalogRow(t, d, "outside")
	if codex.lists.Load() != 1 {
		t.Fatalf("a new Claude session listed Codex too: %d Codex lists", codex.lists.Load())
	}

	before := claude.lists.Load()
	for index := range 40 {
		claude.start(t, "burst-"+strconv.Itoa(index), records)
	}
	awaitCatalogRow(t, d, "burst-39")
	time.Sleep(300 * time.Millisecond)
	if passes := claude.lists.Load() - before; passes < 1 || passes > 3 {
		t.Fatalf("40 sessions starting at once took %d discovery passes; they coalesce into a pass and at most one more", passes)
	}
}

func TestANewDatedFolderIsWatchedBeforeItsFirstSessionLands(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "22"), 0700); err != nil {
		t.Fatal(err)
	}
	codex := newWatchingSurface(surface.KindCodex, func() []string {
		dirs := []string{root}
		if newest := newestChild(root); newest != "" {
			dirs = append(dirs, newest)
		}
		return dirs
	})
	d.Surfaces = []surface.Surface{codex}
	runCatalogLoop(t, d)
	awaitFederation(t, "the first discovery pass", func() bool { return codex.lists.Load() == 1 })

	day := filepath.Join(root, "23")
	if err := os.Mkdir(day, 0700); err != nil {
		t.Fatal(err)
	}
	awaitFederation(t, "the pass the new folder starts", func() bool { return codex.lists.Load() == 2 })
	codex.start(t, "after-midnight", day)
	awaitCatalogRow(t, d, "after-midnight")
}

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

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type watchingSurface struct {
	*daemonSurface
	dirs   func() []string
	mu     sync.Mutex
	added  []surface.Session
	lists  atomic.Int32
	listed func()
}

func newWatchingSurface(kind surface.SurfaceKind, dirs func() []string) *watchingSurface {
	return &watchingSurface{daemonSurface: &daemonSurface{kind: kind, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}}, dirs: dirs}
}

func (w *watchingSurface) CatalogWatchDirs() []string { return w.dirs() }

func (w *watchingSurface) List(context.Context) ([]surface.Session, error) {
	w.lists.Add(1)
	w.mu.Lock()
	sessions := append([]surface.Session{}, w.added...)
	listed := w.listed
	w.listed = nil
	w.mu.Unlock()
	if listed != nil {
		listed()
	}
	return sessions, nil
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
	testkit.Eventually(t, "the first discovery pass", 10*time.Second, func() bool { return claude.lists.Load() == 1 && codex.lists.Load() == 1 })

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
	if passes := claude.lists.Load() - before; passes < 1 || passes > 3 {
		t.Fatalf("40 sessions starting at once took %d discovery passes; they coalesce into a pass and at most one more", passes)
	}
}

func TestManyWakesBeforeAPassAskForOnePassOfEachSurface(t *testing.T) {
	wakes := newCatalogWakes(nil, nil)
	for range 40 {
		wakes.request(surface.KindClaude)
	}
	wakes.request(surface.KindCodex)
	if len(wakes.signal) != 1 {
		t.Fatalf("%d signals pending, want one", len(wakes.signal))
	}
	<-wakes.signal
	if taken := wakes.take(); len(taken) != 2 || !taken[surface.KindClaude] || !taken[surface.KindCodex] {
		t.Fatalf("took %v, want one pass of each surface", taken)
	}
	if taken := wakes.take(); len(taken) != 0 {
		t.Fatalf("took %v again", taken)
	}
}

func TestASessionStartedDuringTheFirstPassIsNotLeftForTheTicker(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	records := t.TempDir()
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	claude.listed = func() { claude.start(t, "during-the-pass", records) }
	d.Surfaces = []surface.Surface{claude}
	runCatalogLoop(t, d)
	awaitCatalogRow(t, d, "during-the-pass")
}

func TestASessionsFolderCreatedLaterIsWatchedFromItsParent(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	root := t.TempDir()
	records := filepath.Join(root, "sessions")
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	d.Surfaces = []surface.Surface{claude}
	runCatalogLoop(t, d)
	testkit.Eventually(t, "the first discovery pass", 10*time.Second, func() bool { return claude.lists.Load() == 1 })

	if err := os.MkdirAll(records, 0700); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the pass the new folder starts", 10*time.Second, func() bool { return claude.lists.Load() >= 2 })
	claude.start(t, "first-ever", records)
	awaitCatalogRow(t, d, "first-ever")
}

func TestASyncThatFindsAnAwaitedFolderAsksForItsPass(t *testing.T) {
	records := filepath.Join(t.TempDir(), "sessions")
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	watcher := filewake.New(filewake.DefaultLimit, t.Logf)
	wakes := newCatalogWakes([]surface.Surface{claude}, watcher.FollowDir)
	t.Cleanup(wakes.close)
	wakes.sync()
	// The parent's wake for the new folder never arrives, as when the loop syncs before it is forwarded.
	if err := watcher.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(records, 0700); err != nil {
		t.Fatal(err)
	}
	wakes.sync()
	select {
	case <-wakes.signal:
	default:
		t.Fatal("a sync that found the awaited folder dropped its parent's watch without asking for a pass")
	}
	if taken := wakes.take(); !taken[surface.KindClaude] {
		t.Fatalf("took %v, want a Claude pass", taken)
	}
}

func TestASyncThatReadTheFoldersBeforeOneAppearedDoesNotUndoANewerSync(t *testing.T) {
	records := filepath.Join(t.TempDir(), "sessions")
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	read, newer := make(chan struct{}), make(chan struct{})
	var held atomic.Bool
	codex := newWatchingSurface(surface.KindCodex, func() []string {
		if held.CompareAndSwap(false, true) {
			close(read)
			select {
			case <-newer:
			case <-time.After(200 * time.Millisecond):
			}
		}
		return nil
	})
	watcher := filewake.New(filewake.DefaultLimit, t.Logf)
	t.Cleanup(func() { _ = watcher.Close() })
	wakes := newCatalogWakes([]surface.Surface{claude, codex}, watcher.FollowDir)
	t.Cleanup(wakes.close)
	stale := make(chan struct{})
	go func() {
		defer close(stale)
		wakes.sync()
	}()
	<-read
	if err := os.Mkdir(records, 0700); err != nil {
		t.Fatal(err)
	}
	wakes.sync()
	close(newer)
	<-stale
	wakes.mu.Lock()
	_, followed := wakes.watching[records]
	wakes.mu.Unlock()
	if !followed {
		t.Fatal("a sync that read the folders before the new one appeared dropped its watch after a newer sync followed it")
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
	testkit.Eventually(t, "the first discovery pass", 10*time.Second, func() bool { return codex.lists.Load() == 1 })

	day := filepath.Join(root, "23")
	if err := os.Mkdir(day, 0700); err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the pass the new folder starts", 10*time.Second, func() bool { return codex.lists.Load() >= 2 })
	codex.start(t, "after-midnight", day)
	awaitCatalogRow(t, d, "after-midnight")
}

func TestClosedCatalogWakesFollowNothingMore(t *testing.T) {
	records := t.TempDir()
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{records} })
	follows := 0
	wakes := newCatalogWakes([]surface.Surface{claude}, func(dir string) *filewake.Wake {
		follows++
		return filewake.FollowDir(dir)
	})
	wakes.sync()
	claude.start(t, "while-closing", records)
	wakes.close()
	wakes.sync()
	if follows != 1 {
		t.Fatalf("followed %d times, want nothing followed after close", follows)
	}
}

func TestAFolderWhoseParentIsMissingIsLeftToTheTicker(t *testing.T) {
	root := t.TempDir()
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{filepath.Join(root, "config", "sessions")} })
	followed := []string{}
	wakes := newCatalogWakes([]surface.Surface{claude}, func(dir string) *filewake.Wake {
		followed = append(followed, dir)
		return filewake.FollowDir(dir)
	})
	defer wakes.close()
	wakes.sync()
	if len(followed) != 0 {
		t.Fatalf("followed %v, want no ancestor above the missing folder's parent", followed)
	}
}

func TestAMissingFolderInTheHomeFolderIsLeftToTheTicker(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{filepath.Join(home, "sessions")} })
	followed := []string{}
	wakes := newCatalogWakes([]surface.Surface{claude}, func(dir string) *filewake.Wake {
		followed = append(followed, dir)
		return filewake.FollowDir(dir)
	})
	defer wakes.close()
	wakes.sync()
	if len(followed) != 0 {
		t.Fatalf("followed %v, want the home folder left unwatched", followed)
	}
}

func TestAMissingFolderInALinkToTheHomeFolderIsLeftToTheTicker(t *testing.T) {
	home, links := t.TempDir(), t.TempDir()
	t.Setenv("HOME", home)
	link := filepath.Join(links, "config")
	if err := os.Symlink(home, link); err != nil {
		t.Fatal(err)
	}
	claude := newWatchingSurface(surface.KindClaude, func() []string { return []string{filepath.Join(link, "sessions")} })
	followed := []string{}
	wakes := newCatalogWakes([]surface.Surface{claude}, func(dir string) *filewake.Wake {
		followed = append(followed, dir)
		return filewake.FollowDir(dir)
	})
	defer wakes.close()
	wakes.sync()
	if len(followed) != 0 {
		t.Fatalf("followed %v, want the home folder left unwatched through a link", followed)
	}
}

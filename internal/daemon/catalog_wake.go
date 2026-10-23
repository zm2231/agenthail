package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/zm2231/agenthail/internal/filewake"
	"github.com/zm2231/agenthail/internal/surface"
)

// catalogWakes follows the folders where each surface's new sessions first
// appear. A new entry in one asks for a discovery pass of that surface alone,
// so a session started outside Agenthail reaches the catalog at once rather
// than at the next pass. The discovery ticker stays the backstop for anything
// the platform does not report.
type catalogWakes struct {
	adapters []surface.Surface
	follow   func(string) *filewake.Wake

	mu       sync.Mutex
	closed   bool
	watching map[string]*catalogWake
	pending  map[surface.SurfaceKind]bool
	signal   chan struct{}
	forwards sync.WaitGroup
}

// catalogWake follows one folder. A folder a surface names is followed
// itself; one that does not exist yet is waited for from its parent, which
// asks for a pass only once the folder appears. A higher ancestor, or a
// parent that is the home folder or above it, is left to the ticker: on
// kqueue a followed folder costs a descriptor per entry.
type catalogWake struct {
	kinds  map[surface.SurfaceKind]bool
	named  bool
	awaits []string
	wake   *filewake.Wake
	stop   chan struct{}
}

func newCatalogWakes(adapters []surface.Surface, follow func(string) *filewake.Wake) *catalogWakes {
	return &catalogWakes{adapters: adapters, follow: follow, watching: map[string]*catalogWake{}, pending: map[surface.SurfaceKind]bool{}, signal: make(chan struct{}, 1)}
}

// sync follows the folders the surfaces name now and drops the ones they no
// longer name.
func (c *catalogWakes) sync() {
	wanted := map[string]*catalogWake{}
	want := func(dir string) *catalogWake {
		if wanted[dir] == nil {
			wanted[dir] = &catalogWake{kinds: map[surface.SurfaceKind]bool{}}
		}
		return wanted[dir]
	}
	for _, adapter := range c.adapters {
		watcher, ok := adapter.(surface.CatalogWatcher)
		if !ok {
			continue
		}
		for _, dir := range watcher.CatalogWatchDirs() {
			dir = filepath.Clean(dir)
			if existingDir(dir) {
				watch := want(dir)
				watch.kinds[adapter.Name()] = true
				watch.named = true
				continue
			}
			parent := filepath.Dir(dir)
			if parent == dir || holdsHome(parent) || !existingDir(parent) {
				continue
			}
			watch := want(parent)
			watch.kinds[adapter.Name()] = true
			watch.awaits = append(watch.awaits, dir)
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	for dir, watch := range c.watching {
		if wanted[dir] == nil {
			watch.release()
			delete(c.watching, dir)
		}
	}
	for dir, spec := range wanted {
		if watch, found := c.watching[dir]; found {
			watch.kinds, watch.named, watch.awaits = spec.kinds, spec.named, spec.awaits
			// Re-arms a folder replaced since it was first followed.
			watch.wake.Refresh()
			continue
		}
		wake := c.follow(dir)
		if wake.C() == nil {
			// Past the watch budget or refused by the platform; a later sync
			// tries again and the ticker covers the folder meanwhile.
			wake.Release()
			continue
		}
		spec.wake, spec.stop = wake, make(chan struct{})
		c.watching[dir] = spec
		c.forwards.Add(1)
		go c.forward(spec)
	}
}

// forward turns a folder's wakes into discovery requests for its surfaces.
// It syncs first, so a folder created by the wake, such as a new day's, is
// followed before the pass it starts. A wake of an ancestor that only waits
// for a folder asks for nothing until that folder exists.
func (c *catalogWakes) forward(watch *catalogWake) {
	defer c.forwards.Done()
	for {
		select {
		case <-watch.stop:
			return
		case <-watch.wake.C():
			c.mu.Lock()
			kinds, named, awaits := watch.kinds, watch.named, watch.awaits
			c.mu.Unlock()
			c.sync()
			if !named && !anyExistingDir(awaits) {
				continue
			}
			for kind := range kinds {
				c.request(kind)
			}
		}
	}
}

// request marks a surface for discovery. Requests made before the loop takes
// them collapse into one pass per surface.
func (c *catalogWakes) request(kind surface.SurfaceKind) {
	c.mu.Lock()
	c.pending[kind] = true
	c.mu.Unlock()
	select {
	case c.signal <- struct{}{}:
	default:
	}
}

// take returns the surfaces requested since the last take.
func (c *catalogWakes) take() map[surface.SurfaceKind]bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	taken := c.pending
	c.pending = map[surface.SurfaceKind]bool{}
	return taken
}

func (c *catalogWakes) close() {
	c.mu.Lock()
	c.closed = true
	for dir, watch := range c.watching {
		watch.release()
		delete(c.watching, dir)
	}
	c.mu.Unlock()
	c.forwards.Wait()
}

func (w *catalogWake) release() {
	close(w.stop)
	w.wake.Release()
}

func existingDir(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func anyExistingDir(paths []string) bool {
	for _, path := range paths {
		if existingDir(path) {
			return true
		}
	}
	return false
}

func holdsHome(dir string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	relative, err := filepath.Rel(dir, filepath.Clean(home))
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

package daemon

import (
	"path/filepath"
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
	watching map[string]*catalogWake
	pending  map[surface.SurfaceKind]bool
	signal   chan struct{}
}

type catalogWake struct {
	kind surface.SurfaceKind
	wake *filewake.Wake
	stop chan struct{}
}

func newCatalogWakes(adapters []surface.Surface, follow func(string) *filewake.Wake) *catalogWakes {
	return &catalogWakes{adapters: adapters, follow: follow, watching: map[string]*catalogWake{}, pending: map[surface.SurfaceKind]bool{}, signal: make(chan struct{}, 1)}
}

// sync follows the folders the surfaces name now and drops the ones they no
// longer name.
func (c *catalogWakes) sync() {
	wanted := map[string]surface.SurfaceKind{}
	for _, adapter := range c.adapters {
		watcher, ok := adapter.(surface.CatalogWatcher)
		if !ok {
			continue
		}
		for _, dir := range watcher.CatalogWatchDirs() {
			wanted[filepath.Clean(dir)] = adapter.Name()
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for dir, watch := range c.watching {
		if kind, found := wanted[dir]; !found || kind != watch.kind {
			watch.release()
			delete(c.watching, dir)
		}
	}
	for dir, kind := range wanted {
		if watch, found := c.watching[dir]; found {
			// Arms a folder that did not exist when it was first followed,
			// or one replaced since.
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
		watch := &catalogWake{kind: kind, wake: wake, stop: make(chan struct{})}
		c.watching[dir] = watch
		go c.forward(watch)
	}
}

// forward turns a folder's wakes into discovery requests for its surface. It
// syncs first, so a folder created by the wake, such as a new day's, is
// followed before the pass it starts.
func (c *catalogWakes) forward(watch *catalogWake) {
	for {
		select {
		case <-watch.stop:
			return
		case <-watch.wake.C():
			c.sync()
			c.request(watch.kind)
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
	defer c.mu.Unlock()
	for dir, watch := range c.watching {
		watch.release()
		delete(c.watching, dir)
	}
}

func (w *catalogWake) release() {
	close(w.stop)
	w.wake.Release()
}

package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// newTestDaemon is New for a test that drives the daemon's passes itself.
// Run stops the session sources and waits for the queue workers those passes
// start before it returns; a test has no Run, so its cleanup does both
// instead, before the cleanups registered earlier close the registry they
// write to and before a later test changes the timers a source reads.
func newTestDaemon(t testing.TB, r *registry.Registry, surfaces []surface.Surface) *Daemon {
	d := New(r, surfaces)
	t.Cleanup(d.sources.shutdown)
	t.Cleanup(d.background.stop)
	return d
}

func TestTestSessionSourcesStopWhenTheTestEnds(t *testing.T) {
	var managers []*sessionSourceManager
	t.Run("holds sources", func(t *testing.T) {
		d, reg, fake, from, to := daemonFixture(t)
		replacement := newTestSourceManager(t, reg)
		managers = append(managers, d.sources, replacement)
		if _, err := d.sources.hold(&from, fake, "test"); err != nil {
			t.Fatal(err)
		}
		d.sources = replacement
		if _, err := replacement.hold(&to, fake, "test"); err != nil {
			t.Fatal(err)
		}
	})
	for index, manager := range managers {
		manager.mu.Lock()
		running, closed := len(manager.sources), manager.closed
		manager.mu.Unlock()
		if running != 0 || !closed {
			t.Fatalf("manager %d after the test ended: %d sources running, closed=%v", index, running, closed)
		}
	}
}

// newTestSourceManager is a session source manager for a test. Its cleanup
// shuts the sources down before the cleanups registered earlier close the
// registry they write to.
func newTestSourceManager(t testing.TB, r *registry.Registry) *sessionSourceManager {
	manager := newSessionSourceManager(r)
	t.Cleanup(manager.shutdown)
	return manager
}

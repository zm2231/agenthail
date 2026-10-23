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

func TestATestDaemonStopsItsSessionSourcesWhenTheTestEnds(t *testing.T) {
	var d *Daemon
	t.Run("holds a source", func(t *testing.T) {
		daemon, _, fake, from, _ := daemonFixture(t)
		d = daemon
		if _, err := d.sources.hold(&from, fake, "test"); err != nil {
			t.Fatal(err)
		}
	})
	d.sources.mu.Lock()
	running, closed := len(d.sources.sources), d.sources.closed
	d.sources.mu.Unlock()
	if running != 0 || !closed {
		t.Fatalf("after the test ended: %d sources running, manager closed=%v", running, closed)
	}
}

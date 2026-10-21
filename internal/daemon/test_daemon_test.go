package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// newTestDaemon is New for a test that drives the daemon's passes itself.
// Run waits for the queue workers those passes start before it returns; a
// test has no Run, so its cleanup waits for them instead, before the cleanups
// registered earlier close the registry they write to.
func newTestDaemon(t testing.TB, r *registry.Registry, surfaces []surface.Surface) *Daemon {
	d := New(r, surfaces)
	t.Cleanup(d.background.stop)
	return d
}

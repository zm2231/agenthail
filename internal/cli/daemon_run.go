package cli

import (
	"time"

	"github.com/zm2231/agenthail/internal/buildinfo"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// DaemonRun is what daemon-run hands the daemon once it holds the daemon lock.
type DaemonRun struct {
	Registry    *registry.Registry
	Surfaces    []surface.Surface
	Build       buildinfo.Identity
	HostVersion string
	Began       time.Time
}

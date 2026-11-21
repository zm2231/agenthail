package daemon

import (
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

type catalogRuntimeGaps struct {
	mu   sync.Mutex
	last map[surface.SurfaceKind]string
}

func (d *Daemon) noteCatalogRuntimeGap(kind surface.SurfaceKind, without string) {
	gaps := &d.catalogGaps
	gaps.mu.Lock()
	previous := gaps.last[kind]
	if gaps.last == nil {
		gaps.last = map[surface.SurfaceKind]string{}
	}
	gaps.last[kind] = without
	gaps.mu.Unlock()
	switch {
	case (without == "") == (previous == ""):
	case without == "":
		d.log.Printf("catalog discovery %s: listing with every runtime again", kind)
	default:
		d.log.Printf("catalog discovery %s: listing without %s", kind, without)
	}
}

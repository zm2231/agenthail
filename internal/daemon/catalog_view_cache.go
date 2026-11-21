package daemon

import (
	"context"
	"maps"
	"sync"

	"github.com/zm2231/agenthail/internal/registry"
)

type catalogViewCache struct {
	mu     sync.Mutex
	key    registry.CatalogPresentationKey
	state  *catalogViewState
	builds int
}

// The key is read before the build, so a cached state is never older than the key it is stored under.
func (d *Daemon) loadCatalogViewState() (catalogViewState, error) {
	c := &d.catalogView
	c.mu.Lock()
	defer c.mu.Unlock()
	key, err := d.Registry.ReadCatalogPresentationKey(context.Background())
	if err != nil {
		return catalogViewState{}, err
	}
	if c.state == nil || c.key != key {
		state, err := d.buildCatalogViewState()
		if err != nil {
			return catalogViewState{}, err
		}
		c.key, c.state = key, &state
		c.builds++
	}
	return c.state.clone(), nil
}

func (v catalogViewState) clone() catalogViewState {
	v.archive.Sessions = maps.Clone(v.archive.Sessions)
	v.archive.Projects = maps.Clone(v.archive.Projects)
	v.attention = maps.Clone(v.attention)
	v.marks = maps.Clone(v.marks)
	v.reads = maps.Clone(v.reads)
	v.turnEnds = maps.Clone(v.turnEnds)
	v.asks = maps.Clone(v.asks)
	v.notices = maps.Clone(v.notices)
	v.judgments = maps.Clone(v.judgments)
	v.leads.byProject = maps.Clone(v.leads.byProject)
	v.leads.aliases = maps.Clone(v.leads.aliases)
	return v
}

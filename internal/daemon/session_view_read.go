package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
)

type presentationClock struct {
	sync.Mutex
	epoch        string
	sequence     uint64
	fingerprints presentationFingerprints
	views        []*sessionViewBuild
	hits, builds uint64
	uncached     bool
	clock        func() time.Time
	building     func()
}

func (p *presentationClock) now() time.Time {
	if p.clock != nil {
		return p.clock().UTC()
	}
	return time.Now().UTC()
}

func (d *Daemon) readCatalogPresentation(ctx context.Context, request sessionViewRequest) (registry.CatalogPresentation, error) {
	cache := &d.presentation.fingerprints
	load := d.Registry.ReadCurrentCatalogPresentation
	if request.Scope == "all" || request.Scope == "archived" || request.Filters.Query != "" {
		load = d.Registry.ReadCatalogPresentation
	}
	if d.presentation.uncached {
		read, err := load(ctx, func(string, uint64) bool { return true })
		if err != nil {
			return read, err
		}
		cache.reset(read.HostEpoch)
		return read, cache.adopt(read)
	}
	read, err := load(ctx, cache.stale)
	if err != nil {
		return read, err
	}
	if !cache.valid(read) {
		cache.reset(read.HostEpoch)
		if read, err = load(ctx, cache.stale); err != nil {
			return read, err
		}
		cache.epoch = read.HostEpoch
	}
	return read, cache.adopt(read)
}

func (d *Daemon) sessionViewBuildFor(ctx context.Context, request sessionViewRequest, now time.Time) (*sessionViewBuild, error) {
	if !d.presentation.uncached {
		return d.cachedSessionView(ctx, request, now)
	}
	shared := d.presentation.fingerprints
	d.presentation.fingerprints = presentationFingerprints{}
	defer func() { d.presentation.fingerprints = shared }()
	config, err := LoadDashboardConfig()
	if err != nil {
		return nil, err
	}
	catalog, err := d.Registry.ReadCatalogPresentationKey(ctx)
	if err != nil {
		return nil, err
	}
	return d.buildSessionView(ctx, request, config, d.sessionViewCacheKeyFor(request, catalog, config), now)
}

func (d *Daemon) readSessionView(ctx context.Context, request sessionViewRequest) (SessionViewSnapshot, error) {
	d.presentation.Lock()
	defer d.presentation.Unlock()
	if d.presentation.epoch == "" {
		d.presentation.epoch = uuid.NewString()
	}
	d.presentation.sequence++
	now := d.presentation.now()
	build, err := d.sessionViewBuildFor(ctx, request, now)
	if err != nil {
		return SessionViewSnapshot{}, err
	}
	view := build.view
	view.AsOf = now
	if request.Revision != "" && request.Revision != view.Revision {
		return SessionViewSnapshot{}, errPresentationChanged
	}
	result := SessionViewSnapshot{PresentationPosition: PresentationPosition{HostID: build.key.host.ID, Epoch: d.presentation.epoch, Sequence: d.presentation.sequence}, Sessions: []dashboardSession{}, Counts: map[string]int{}, HostEpoch: build.hostEpoch, CatalogSeq: build.seq}
	result.TotalSessions = view.Counts[request.Scope].Sessions
	for scope, count := range view.Counts {
		result.Counts[scope] = count.Families
	}
	var selected []string
	if len(request.IDs) > 0 {
		allowed := map[string]bool{}
		for _, family := range view.Families {
			for _, member := range family.Members {
				allowed[member.ID] = true
			}
		}
		for _, id := range request.IDs {
			if !allowed[id] {
				return SessionViewSnapshot{}, errPresentationMember
			}
			selected = append(selected, id)
		}
		view.Families = []SessionViewFamily{}
	} else {
		families := view.Families
		if request.Filters.Collapsed {
			families = collapsedFamilies(view)
		}
		rest := familiesAfter(families, request.After)
		end := min(request.Limit, len(rest))
		if end < len(rest) {
			result.NextCursor = encodeSessionViewCursor(request, rest[end-1].key)
		}
		view.Families = rest[:end]
		for _, family := range view.Families {
			if !request.Members {
				selected = append(selected, family.RootID)
				continue
			}
			for _, member := range family.Members {
				selected = append(selected, member.ID)
			}
		}
	}
	for _, id := range selected {
		result.Sessions = append(result.Sessions, *build.rows[id])
	}
	result.View = &view
	return result, nil
}

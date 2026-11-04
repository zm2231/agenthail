package daemon

import (
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// A retired row the read included is already counted once as a row.
func retiredCountOf(row dashboardSession, record registry.CatalogPresentationRow) registry.CatalogRetiredCount {
	count := registry.CatalogRetiredCount{Surface: record.Surface, Archived: record.Archived, Unavailable: record.UnavailableReason != "", Sessions: -1}
	if row.Project != nil {
		count.ProjectID = row.Project.ID
	}
	return count
}

// A skipped retired row is a family of one and needs no attention.
func (p *sessionViewPlan) countRetired(counts []registry.CatalogRetiredCount, filters SessionViewFilters, now time.Time) {
	if filters.Attention {
		return
	}
	add := func(into map[string]SessionViewCount, scope string, sessions int) {
		count := into[scope]
		count.Families += sessions
		count.Sessions += sessions
		into[scope] = count
	}
	for _, retired := range counts {
		if filters.ProjectID != "" && retired.ProjectID != filters.ProjectID {
			continue
		}
		row := dashboardSession{Status: surface.StatusOffline}
		if retired.Unavailable {
			row.Status = surface.StatusUnknown
		}
		if filters.Status != "" && sessionDisplay(row, now).Kind != filters.Status {
			continue
		}
		if filters.Surface != "" && filters.Surface != retired.Surface {
			continue
		}
		scope := "all"
		if retired.Archived {
			scope = "archived"
		}
		add(p.view.Counts, scope, retired.Sessions)
		at := sort.Search(len(p.view.Surfaces), func(i int) bool { return p.view.Surfaces[i].Surface >= retired.Surface })
		if at == len(p.view.Surfaces) || p.view.Surfaces[at].Surface != retired.Surface {
			summary := SessionViewSurface{Surface: retired.Surface, Counts: map[string]SessionViewCount{}}
			p.view.Surfaces = append(p.view.Surfaces[:at], append([]SessionViewSurface{summary}, p.view.Surfaces[at:]...)...)
		}
		add(p.view.Surfaces[at].Counts, scope, retired.Sessions)
	}
}

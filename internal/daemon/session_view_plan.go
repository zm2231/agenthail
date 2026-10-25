package daemon

import (
	"sort"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type sessionViewPlan struct {
	view     SessionView
	rows     map[string]dashboardSession
	roots    map[string]string
	children map[string][]string
	working  map[string]int
	now      time.Time
}

func presentationCurrent(row dashboardSession) bool {
	return !row.Archived && (row.Current && row.CurrentReason != "queued" || presentationWorking(row) || row.QueueCount > 0 || row.WaitingOn != nil || row.Attention != nil)
}

func presentationMatches(row dashboardSession, scope string, now time.Time) bool {
	if scope == "archived" {
		return row.Archived
	}
	if row.Archived {
		return false
	}
	switch scope {
	case "all":
		return true
	case "running":
		return presentationWorking(row)
	case "current":
		return presentationCurrent(row)
	case "recent":
		return presentationCurrent(row) || !row.LastActive.IsZero() && !row.LastActive.Before(now.Add(-24*time.Hour))
	}
	return false
}

func presentationPinned(row dashboardSession) bool {
	return presentationWorking(row) || row.QueueCount > 0 || row.WaitingOn != nil || row.Attention != nil
}

func newSessionViewPlan(rows []dashboardSession, scope string, filters SessionViewFilters, now time.Time) sessionViewPlan {
	p := sessionViewPlan{view: SessionView{Scope: scope, Filters: filters, AsOf: now, Counts: map[string]SessionViewCount{}, Projects: []SessionViewProject{}, Families: []SessionViewFamily{}, Surfaces: []SessionViewSurface{}}, rows: map[string]dashboardSession{}, roots: map[string]string{}, children: map[string][]string{}, working: map[string]int{}, now: now}
	for _, row := range rows {
		if row.Display == nil {
			display := sessionDisplay(row, now)
			row.Display = &display
		}
		p.rows[row.ID] = row
	}
	for _, row := range rows {
		seen := map[string]bool{}
		at := row.ID
		for at != "" && !seen[at] {
			seen[at] = true
			parent, found := p.rows[at]
			if !found {
				break
			}
			if parent.Subagent == nil {
				if !parent.Delegated && parent.Source != "subAgent" {
					p.roots[row.ID] = at
				}
				break
			}
			at = parent.Subagent.ParentID
		}
		if row.Subagent != nil && p.roots[row.ID] != "" {
			p.children[row.Subagent.ParentID] = append(p.children[row.Subagent.ParentID], row.ID)
		}
		p.deadlines(row)
	}
	for id, root := range p.roots {
		row := p.rows[id]
		if id != root && row.Status == surface.StatusBusy {
			p.working[root]++
		}
		if row.ObservedSubagents != nil {
			p.working[root] += row.ObservedSubagents.Working
		}
	}
	for parent := range p.children {
		sort.Slice(p.children[parent], func(i, j int) bool {
			return presentationBefore(p.rows[p.children[parent][i]], p.rows[p.children[parent][j]])
		})
	}
	for _, candidate := range catalogScopes {
		families := p.families(candidate, filters)
		p.view.Counts[candidate] = countViewFamilies(families)
		if candidate == scope {
			p.view.Families = families
		}
	}
	p.view.Summary = p.summary(p.view.Families)
	kinds := map[string]bool{}
	for _, row := range rows {
		kinds[string(row.Surface)] = true
	}
	for kind := range kinds {
		summary := SessionViewSurface{Surface: kind, Counts: map[string]SessionViewCount{}}
		if filters.Surface != "" && filters.Surface != kind {
			continue
		}
		f := filters
		f.Surface = kind
		for _, candidate := range catalogScopes {
			families := p.families(candidate, f)
			summary.Counts[candidate] = countViewFamilies(families)
			if candidate == scope {
				summary.Summary = p.summary(families)
			}
		}
		p.view.Surfaces = append(p.view.Surfaces, summary)
	}
	sort.Slice(p.view.Surfaces, func(i, j int) bool { return p.view.Surfaces[i].Surface < p.view.Surfaces[j].Surface })
	p.orderProjects()
	return p
}

func presentationBefore(a, b dashboardSession) bool {
	if !a.LastActive.Equal(b.LastActive) {
		return a.LastActive.After(b.LastActive)
	}
	return a.ID < b.ID
}

func (p *sessionViewPlan) qualifies(row dashboardSession, scope string, filters SessionViewFilters) bool {
	root := p.rows[p.roots[row.ID]]
	if root.ID == "" {
		return false
	}
	if row.ID != root.ID && !row.Archived && !presentationMatches(row, "recent", p.now) {
		return false
	}
	if filters.ProjectID != "" && (root.Project == nil || root.Project.ID != filters.ProjectID) {
		return false
	}
	return presentationMatches(row, scope, p.now) && (filters.Surface == "" || string(row.Surface) == filters.Surface) && (filters.Status == "" || row.Display.Kind == filters.Status) && (!filters.Attention || row.Attention != nil) && (filters.Query == "" || strings.Contains(presentationSearch(row), filters.Query))
}

func (p *sessionViewPlan) families(scope string, filters SessionViewFilters) []SessionViewFamily {
	matched := map[string]bool{}
	needed := map[string]bool{}
	for id, row := range p.rows {
		if !p.qualifies(row, scope, filters) {
			continue
		}
		matched[id] = true
		for at := id; at != "" && !needed[at]; {
			needed[at] = true
			row := p.rows[at]
			if row.Subagent == nil {
				break
			}
			at = row.Subagent.ParentID
		}
	}
	families := []SessionViewFamily{}
	for id, row := range p.rows {
		if p.roots[id] != id || !needed[id] {
			continue
		}
		family := SessionViewFamily{ID: id, RootID: id, Members: []SessionViewMember{}}
		if row.Project != nil {
			family.ProjectID = row.Project.ID
		}
		if row.Checkout != nil {
			family.CheckoutID = row.Checkout.ID
		}
		var walk func(string, int)
		walk = func(at string, depth int) {
			if !needed[at] {
				return
			}
			member := p.rows[at]
			pinned := matched[at] && presentationPinned(member)
			family.Members = append(family.Members, SessionViewMember{ID: at, Depth: depth, ContextOnly: !matched[at], Pinned: pinned})
			family.Pinned = family.Pinned || pinned
			if matched[at] && member.LastActive.After(family.activity) {
				family.activity = member.LastActive
			}
			for _, child := range p.children[at] {
				walk(child, depth+1)
			}
		}
		walk(id, 0)
		family.WorkingSubagents = p.working[id]
		families = append(families, family)
	}
	return families
}

func countViewFamilies(families []SessionViewFamily) SessionViewCount {
	count := SessionViewCount{Families: len(families)}
	for _, family := range families {
		for _, member := range family.Members {
			if !member.ContextOnly {
				count.Sessions++
			}
		}
	}
	return count
}

func (p *sessionViewPlan) summary(families []SessionViewFamily) SessionViewSummary {
	var result SessionViewSummary
	for _, family := range families {
		attention := false
		for _, member := range family.Members {
			if member.ContextOnly {
				continue
			}
			row := p.rows[member.ID]
			result.QueuedMessages += row.QueueCount
			attention = attention || row.Attention != nil
		}
		if attention {
			result.AttentionFamilies++
		}
	}
	return result
}

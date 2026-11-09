package daemon

import (
	"slices"
	"sort"

	"github.com/zm2231/agenthail/internal/surface"
)

func (p *sessionViewPlan) link() {
	for id, row := range p.rows {
		if row.Subagent != nil {
			p.parent[id] = row.Subagent.ParentID
			continue
		}
		starter := p.rows[row.StartedBy]
		if row.StartedBy == "" || starter == nil || starter.Archived || !sameFamilyPlace(row, starter) {
			continue
		}
		p.parent[id], p.worker[id] = row.StartedBy, true
	}
	ids := make([]string, 0, len(p.parent))
	for id := range p.parent {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	const visiting, done = 1, 2
	state := map[string]int{}
	for _, id := range ids {
		var path []string
		at := id
		for at != "" && state[at] == 0 {
			state[at] = visiting
			path = append(path, at)
			at = p.parent[at]
		}
		if at != "" && state[at] == visiting {
			p.cutWorkerLink(path[slices.Index(path, at):])
		}
		for _, visited := range path {
			state[visited] = done
		}
	}
}

func (p *sessionViewPlan) cutWorkerLink(cycle []string) {
	for _, id := range slices.Sorted(slices.Values(cycle)) {
		if p.worker[id] {
			delete(p.parent, id)
			delete(p.worker, id)
			return
		}
	}
}

func (p *sessionViewPlan) up(id string, matched map[string]bool) string {
	parent := p.parent[id]
	if p.worker[id] && !matched[parent] {
		return ""
	}
	return parent
}

func (p *sessionViewPlan) workingSubagents(matched map[string]bool) map[string]int {
	rootOf := map[string]string{}
	var root func(string) string
	root = func(id string) string {
		if known, ok := rootOf[id]; ok {
			return known
		}
		found := id
		if parent := p.up(id, matched); parent != "" {
			found = root(parent)
		}
		rootOf[id] = found
		return found
	}
	working := map[string]int{}
	for id, row := range p.rows {
		if p.roots[id] == "" {
			continue
		}
		at := root(id)
		if id != at && row.Subagent != nil && row.Status == surface.StatusBusy {
			working[at]++
		}
		if row.ObservedSubagents != nil {
			working[at] += row.ObservedSubagents.Working
		}
	}
	return working
}

func rollUp(rollup *SessionViewWorkers, member *dashboardSession) *SessionViewWorkers {
	if rollup == nil {
		rollup = &SessionViewWorkers{}
	}
	rollup.Count++
	if presentationWorking(member) {
		rollup.Working++
	}
	if presentationNeedsYou(member) {
		rollup.NeedsYou++
	}
	return rollup
}

func sameFamilyPlace(a, b *dashboardSession) bool {
	if a.Checkout == nil || b.Checkout == nil || a.Checkout.ID == "" || a.Checkout.ID != b.Checkout.ID {
		return false
	}
	project := func(row *dashboardSession) string {
		if row.Project == nil {
			return ""
		}
		return row.Project.ID
	}
	return project(a) == project(b)
}

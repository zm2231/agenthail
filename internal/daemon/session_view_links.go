package daemon

import (
	"slices"
	"sort"
)

func (p *sessionViewPlan) link() {
	for id, row := range p.rows {
		if row.Subagent != nil {
			p.parent[id] = row.Subagent.ParentID
			continue
		}
		starter := p.rows[row.StartedBy]
		if row.StartedBy == "" || starter == nil || !sameFamilyPlace(row, starter) {
			continue
		}
		p.parent[id], p.worker[id] = row.StartedBy, true
	}
	ids := make([]string, 0, len(p.parent))
	for id := range p.parent {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		index := map[string]int{}
		var path []string
		for at := id; at != ""; at = p.parent[at] {
			if i, seen := index[at]; seen {
				p.cutWorkerLink(path[i:])
				break
			}
			index[at] = len(path)
			path = append(path, at)
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

package daemon

import (
	"sort"
	"time"
)

func (p *sessionViewPlan) deadline(at time.Time) {
	if at.After(p.now) && (p.view.RefreshAt == nil || at.Before(*p.view.RefreshAt)) {
		p.view.RefreshAt = displayInstant(at)
	}
}

func (p *sessionViewPlan) deadlines(row dashboardSession) {
	if !row.LastActive.IsZero() {
		p.deadline(row.LastActive.Add(24*time.Hour + time.Nanosecond))
	}
	if row.UsageLimit != nil {
		p.deadline(row.UsageLimit.ResetsAt)
	}
	if row.Activity != nil {
		last := row.Activity.LastProgressAt
		if last.IsZero() {
			last = row.Activity.BusySince
		}
		if !last.IsZero() {
			p.deadline(last.Add(10 * time.Minute))
		}
	}
}

func (p *sessionViewPlan) orderProjects() {
	projects := map[string]*SessionViewProject{}
	for _, family := range p.view.Families {
		project := projects[family.ProjectID]
		if project == nil {
			name := "No project"
			if identity := p.rows[family.ID].Project; identity != nil {
				name = identity.DisplayName
			}
			project = &SessionViewProject{ID: family.ProjectID, Name: name, CollapsedFamilyIDs: []string{}}
			projects[family.ProjectID] = project
		}
		count := countViewFamilies([]SessionViewFamily{family})
		project.Counts.Families += count.Families
		project.Counts.Sessions += count.Sessions
		if family.activity.After(project.activity) {
			project.activity = family.activity
		}
	}
	for _, project := range projects {
		p.view.Projects = append(p.view.Projects, *project)
	}
	sort.Slice(p.view.Projects, func(i, j int) bool {
		a, b := p.view.Projects[i], p.view.Projects[j]
		if (a.ID == "") != (b.ID == "") {
			return a.ID != ""
		}
		if !a.activity.Equal(b.activity) {
			return a.activity.After(b.activity)
		}
		return a.ID < b.ID
	})
	rank := map[string]int{}
	for i, project := range p.view.Projects {
		rank[project.ID] = i
	}
	sort.Slice(p.view.Families, func(i, j int) bool {
		a, b := p.view.Families[i], p.view.Families[j]
		if a.ProjectID != b.ProjectID {
			return rank[a.ProjectID] < rank[b.ProjectID]
		}
		if a.Pinned != b.Pinned {
			return a.Pinned
		}
		if !a.activity.Equal(b.activity) {
			return a.activity.After(b.activity)
		}
		return a.ID < b.ID
	})
	ordinary := map[string]int{}
	for _, family := range p.view.Families {
		selected := false
		for _, member := range family.Members {
			selected = selected || member.ID == p.view.Filters.SelectedID
		}
		if !family.Pinned {
			ordinary[family.ProjectID]++
		}
		if family.Pinned || ordinary[family.ProjectID] <= 5 || selected {
			index := rank[family.ProjectID]
			p.view.Projects[index].CollapsedFamilyIDs = append(p.view.Projects[index].CollapsedFamilyIDs, family.ID)
		}
	}
}

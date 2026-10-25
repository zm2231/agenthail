package daemon

import "strings"

func addEmptyPresentationProjects(plan *sessionViewPlan, projects []dashboardProject) {
	view := &plan.view
	if view.Scope != "all" && view.Scope != "archived" || view.Filters.Surface != "" || view.Filters.Status != "" || view.Filters.Attention {
		return
	}
	seen := map[string]bool{}
	for _, project := range view.Projects {
		seen[project.ID] = true
	}
	for _, project := range projects {
		if seen[project.ID] || project.Archived != (view.Scope == "archived") {
			continue
		}
		if view.Filters.ProjectID != "" && view.Filters.ProjectID != project.ID {
			continue
		}
		if view.Filters.Query != "" && !strings.Contains(strings.ToLower(project.DisplayName), view.Filters.Query) {
			continue
		}
		view.Projects = append(view.Projects, SessionViewProject{ID: project.ID, Name: project.DisplayName, CollapsedFamilyIDs: []string{}})
	}
	return
}

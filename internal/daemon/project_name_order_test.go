package daemon

import (
	"reflect"
	"testing"
	"time"
)

func TestCatalogProjectsAreListedByNameNotActivity(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	local := dashboardHost{ID: "here", Name: "here", State: "connected"}
	row := func(id, project, name string, ago time.Duration) dashboardSession {
		return dashboardSession{ID: id, Project: &CatalogProject{ID: project, LocalID: project, DisplayName: name}, LastActive: now.Add(-ago)}
	}
	listed := func(rows ...dashboardSession) []string {
		var names []string
		for _, project := range catalogProjects(rows, nil, nil, nil, local, func(string) dashboardHost { return local }) {
			names = append(names, project.DisplayName)
		}
		return names
	}
	want := []string{"Alpha", "alpha", "beta", "Gamma"}
	first := listed(row("a", "p1", "beta", time.Minute), row("b", "p2", "Gamma", time.Hour), row("c", "p3", "Alpha", 3*time.Hour), row("d", "p4", "alpha", 2*time.Hour))
	if !reflect.DeepEqual(first, want) {
		t.Fatalf("projects=%v, want %v", first, want)
	}
	touched := listed(row("a", "p1", "beta", 5*time.Hour), row("b", "p2", "Gamma", time.Hour), row("c", "p3", "Alpha", time.Second), row("d", "p4", "alpha", 2*time.Hour))
	if !reflect.DeepEqual(touched, want) {
		t.Fatalf("after activity moved, projects=%v, want %v", touched, want)
	}
}

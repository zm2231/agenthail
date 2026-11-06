package daemon

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestIdleFamiliesFillWhatTheActiveOnesLeaveOfFive(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	project := &CatalogProject{ID: "project", DisplayName: "Project"}
	rows := []dashboardSession{
		{ID: "working-a", Status: surface.StatusBusy, Project: project, LastActive: now.Add(-time.Hour), RegisteredAt: now.Add(-2 * time.Hour)},
		{ID: "held-a", Status: surface.StatusIdle, Project: project, LastActive: now.Add(-2 * time.Minute), RegisteredAt: now.Add(-4 * time.Hour)},
	}
	for i := range 6 {
		rows = append(rows, dashboardSession{ID: fmt.Sprint("idle-", i), Status: surface.StatusIdle, Project: project, LastActive: now.Add(-time.Hour - time.Duration(i)*time.Minute)})
	}
	collapsed := func() []string {
		return newSessionViewPlan(rows, "recent", SessionViewFilters{}, now).view.Projects[0].CollapsedFamilyIDs
	}
	if got, want := collapsed(), []string{"working-a", "held-a", "idle-0", "idle-1", "idle-2"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("collapsed=%v want=%v", got, want)
	}

	for i := range 6 {
		rows = append(rows, dashboardSession{ID: fmt.Sprint("working-", i), Status: surface.StatusBusy, Project: project, LastActive: now.Add(-time.Hour), RegisteredAt: now.Add(-time.Duration(10+i) * time.Hour)})
	}
	if got, want := collapsed(), []string{"working-a", "held-a", "working-0", "working-1", "working-2", "working-3", "working-4", "working-5"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("every active family stays shown and no idle one is added past five: %v", got)
	}
}

func TestTheCollapsedListingHoldsEachProjectsCollapsedFamiliesInViewOrder(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rows := []dashboardSession{}
	for _, id := range []string{"a", "b"} {
		project := &CatalogProject{ID: id, DisplayName: id}
		for i := range 7 {
			rows = append(rows, dashboardSession{ID: fmt.Sprint(id, i), Status: surface.StatusIdle, Project: project, LastActive: now.Add(-time.Hour - time.Duration(i)*time.Minute)})
		}
	}
	plan := newSessionViewPlan(rows, "all", SessionViewFilters{Collapsed: true}, now)
	ids := []string{}
	for _, family := range collapsedFamilies(plan.view) {
		ids = append(ids, family.ID)
	}
	if want := []string{"a0", "b0", "a1", "b1", "a2", "b2", "a3", "b3", "a4", "b4"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("collapsed listing=%v want=%v", ids, want)
	}
	if plan.view.Counts["all"].Families != 14 {
		t.Fatalf("the collapsed listing changed the counts: %+v", plan.view.Counts)
	}
}

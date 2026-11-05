package daemon

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type orderFixture struct {
	now  time.Time
	rows map[string]*dashboardSession
	ids  []string
}

func newOrderFixture() *orderFixture {
	f := &orderFixture{now: time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC), rows: map[string]*dashboardSession{}}
	projects := []*CatalogProject{{ID: "p-beta", DisplayName: "beta"}, {ID: "p-alpha", DisplayName: "Alpha"}, {ID: "p-gamma", DisplayName: "gamma"}}
	for i := range 18 {
		project := projects[i%3]
		row := &dashboardSession{ID: fmt.Sprintf("s%02d", i), Surface: surface.KindClaude, Project: project, Status: surface.StatusIdle, RegisteredAt: f.now.Add(-time.Duration(30+i) * time.Hour), LastActive: f.now.Add(-time.Duration(2+i) * time.Hour)}
		if i%3 != 2 && i < 12 {
			row.Status, row.Current, row.LastActive = surface.StatusBusy, true, f.now.Add(-time.Duration(i)*time.Second)
			row.Activity = &surface.Activity{BusySince: f.now.Add(-time.Minute), LastProgressAt: row.LastActive}
		}
		f.add(row)
	}
	f.add(&dashboardSession{ID: "child", Surface: surface.KindClaude, Status: surface.StatusBusy, RegisteredAt: f.now.Add(-time.Hour), LastActive: f.now, Activity: &surface.Activity{BusySince: f.now}, Subagent: &surface.Subagent{ParentID: "s00", RootID: "s00"}})
	f.add(&dashboardSession{ID: "loose", Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: f.now.Add(-5 * time.Hour)})
	return f
}

func (f *orderFixture) add(row *dashboardSession) {
	f.rows[row.ID] = row
	f.ids = append(f.ids, row.ID)
}

type viewOrder struct {
	Projects  []string
	Families  []string
	Collapsed map[string][]string
}

func (f *orderFixture) order(t *testing.T, scope string) viewOrder {
	t.Helper()
	rows := make([]dashboardSession, 0, len(f.ids))
	for _, id := range f.ids {
		rows = append(rows, *f.rows[id])
	}
	plan := newSessionViewPlan(rows, scope, SessionViewFilters{}, f.now)
	order := viewOrder{Collapsed: map[string][]string{}}
	for _, project := range plan.view.Projects {
		order.Projects = append(order.Projects, project.ID)
		order.Collapsed[project.ID] = project.CollapsedFamilyIDs
	}
	for _, family := range plan.view.Families {
		order.Families = append(order.Families, family.ID)
	}
	return order
}

func (f *orderFixture) progress(step int) {
	f.now = f.now.Add(5 * time.Second)
	for _, id := range f.ids {
		row := f.rows[id]
		if row.Status != surface.StatusBusy {
			continue
		}
		row.LastActive = f.now.Add(-time.Duration((step*7+len(id))%5) * time.Second)
		row.Activity = &surface.Activity{BusySince: row.Activity.BusySince, LastProgressAt: row.LastActive, Tool: &surface.RunningTool{Name: fmt.Sprint("tool", step%4), StartedAt: f.now}}
	}
	flapping := f.rows[fmt.Sprintf("s%02d", (step%4)*3)]
	if step%2 == 0 {
		flapping.Status, flapping.Activity, flapping.LastActive = surface.StatusIdle, nil, f.now
	} else {
		flapping.Status, flapping.Activity = surface.StatusBusy, &surface.Activity{BusySince: f.now, LastProgressAt: f.now}
	}
}

func TestSessionViewOrderHoldsStillWhileWorkRuns(t *testing.T) {
	for _, scope := range []string{"current", "recent", "all"} {
		f := newOrderFixture()
		want := f.order(t, scope)
		expected := "[p-alpha p-beta p-gamma ]"
		if scope == "current" {
			expected = "[p-alpha p-beta]"
		}
		if fmt.Sprint(want.Projects) != expected {
			t.Fatalf("%s: projects %v", scope, want.Projects)
		}
		for step := range 100 {
			f.progress(step)
			if step == 50 {
				f.add(&dashboardSession{ID: "late-child", Surface: surface.KindClaude, Status: surface.StatusBusy, RegisteredAt: f.now, LastActive: f.now, Activity: &surface.Activity{BusySince: f.now}, Subagent: &surface.Subagent{ParentID: "s03", RootID: "s03"}})
			}
			if got := f.order(t, scope); !reflect.DeepEqual(got, want) {
				t.Fatalf("%s step %d moved:\n got %+v\nwant %+v", scope, step, got, want)
			}
		}
	}
}

func TestSessionViewOrderMovesOnlyOnNews(t *testing.T) {
	f := newOrderFixture()
	before := f.order(t, "current")
	f.rows["s04"].WaitingOn, f.rows["s04"].Attention = &surface.Waiting{ID: "q", Since: f.now}, &SessionAttention{Kind: "request"}
	asked := f.order(t, "current")
	if asked.Collapsed["p-alpha"][0] != "s04" || !reflect.DeepEqual(asked.Projects, before.Projects) {
		t.Fatalf("a waiting row did not go to the top of its project: %+v", asked)
	}
	f.now = f.now.Add(time.Minute)
	f.rows["s01"].WaitingOn, f.rows["s01"].Attention = &surface.Waiting{ID: "q2", Since: f.now}, &SessionAttention{Kind: "request"}
	if got := f.order(t, "current").Collapsed["p-alpha"][:2]; fmt.Sprint(got) != "[s04 s01]" {
		t.Fatalf("a newer wait went ahead of the older one: %v", got)
	}

	f = newOrderFixture()
	projects := func() string { return fmt.Sprint(f.order(t, "all").Projects) }
	for _, id := range []string{"s01", "s04", "s07", "s10"} {
		f.rows[id].Status, f.rows[id].Activity, f.rows[id].LastActive = surface.StatusIdle, nil, f.now
	}
	if got := projects(); got != "[p-alpha p-beta p-gamma ]" {
		t.Fatalf("alpha left the active group when its sessions finished: %v", got)
	}
	f.now = f.now.Add(sessionHold - time.Second)
	if got := projects(); got != "[p-alpha p-beta p-gamma ]" {
		t.Fatalf("alpha left the active group before its hold ended: %v", got)
	}
	f.now = f.now.Add(2 * time.Second)
	if got := projects(); got != "[p-beta p-alpha p-gamma ]" {
		t.Fatalf("alpha stayed in the active group after its hold ended: %v", got)
	}
	f.rows["s05"].Status, f.rows["s05"].Activity = surface.StatusBusy, &surface.Activity{BusySince: f.now}
	if got := projects(); got != "[p-beta p-gamma p-alpha ]" {
		t.Fatalf("gamma did not join the active group when its first session started: %v", got)
	}
}

func TestSessionViewHoldSetsARefreshDeadline(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	rows := []dashboardSession{{ID: "done", Status: surface.StatusIdle, LastActive: now.Add(-time.Minute)}}
	plan := newSessionViewPlan(rows, "current", SessionViewFilters{}, now)
	if plan.view.RefreshAt == nil || !plan.view.RefreshAt.Equal(now.Add(sessionHold-time.Minute)) {
		t.Fatalf("refreshAt=%v", plan.view.RefreshAt)
	}
}

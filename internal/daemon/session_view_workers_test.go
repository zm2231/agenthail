package daemon

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type workerFixture struct {
	now  time.Time
	rows []dashboardSession
}

func newWorkerFixture() *workerFixture {
	return &workerFixture{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
}

func (f *workerFixture) add(id, startedBy, checkout string, edit ...func(*dashboardSession)) {
	row := dashboardSession{ID: id, Surface: surface.KindCodex, Status: surface.StatusIdle, StartedBy: startedBy, Project: &CatalogProject{ID: "project", DisplayName: "Project"}, Checkout: &catalogCheckout{ID: checkout}, LastActive: f.now.Add(-time.Minute), RegisteredAt: f.now.Add(-time.Hour)}
	for _, e := range edit {
		e(&row)
	}
	f.rows = append(f.rows, row)
}

func (f *workerFixture) plan(filters SessionViewFilters) sessionViewPlan {
	return newSessionViewPlan(f.rows, "all", filters, f.now)
}

func familyOf(t *testing.T, plan sessionViewPlan, id string) (SessionViewFamily, SessionViewMember) {
	t.Helper()
	var family SessionViewFamily
	var member SessionViewMember
	found := 0
	for _, candidate := range plan.view.Families {
		for _, m := range candidate.Members {
			if m.ID == id {
				family, member = candidate, m
				found++
			}
		}
	}
	if found != 1 {
		t.Fatalf("%s is in %d families, want exactly 1: %+v", id, found, plan.view.Families)
	}
	return family, member
}

func TestAWorkerFoldsUnderTheSessionThatStartedItInTheSameCheckout(t *testing.T) {
	f := newWorkerFixture()
	f.add("lead", "", "main")
	f.add("worker", "lead", "main", func(r *dashboardSession) { r.Status = surface.StatusBusy })
	f.add("worker-of-worker", "worker", "main")
	f.add("asking", "lead", "main", func(r *dashboardSession) { r.Attention = &SessionAttention{Kind: AttentionAsk} })
	f.add("elsewhere", "lead", "topic")
	f.add("subagent", "", "main", func(r *dashboardSession) {
		r.Status, r.Subagent = surface.StatusBusy, &surface.Subagent{ParentID: "lead", RootID: "lead", Depth: 1}
	})
	plan := f.plan(SessionViewFilters{})

	lead, root := familyOf(t, plan, "lead")
	if lead.RootID != "lead" || root.Depth != 0 || root.Worker {
		t.Fatalf("the starter's family is %+v", lead)
	}
	for id, depth := range map[string]int{"worker": 1, "asking": 1, "worker-of-worker": 2} {
		family, member := familyOf(t, plan, id)
		if family.RootID != "lead" || member.Depth != depth || !member.Worker {
			t.Fatalf("%s is %+v in %s, want a worker at depth %d under lead", id, member, family.RootID, depth)
		}
	}
	if family, member := familyOf(t, plan, "elsewhere"); family.RootID != "elsewhere" || member.Worker {
		t.Fatalf("a worker in another checkout folded: %+v", family)
	}
	if family, member := familyOf(t, plan, "subagent"); family.RootID != "lead" || member.Worker {
		t.Fatalf("a subagent is %+v", member)
	}
	if lead.Workers == nil || *lead.Workers != (SessionViewWorkers{Count: 3, Working: 1, NeedsYou: 1}) {
		t.Fatalf("workers=%+v, want 3 with 1 working and 1 needing you", lead.Workers)
	}
	if lead.WorkingSubagents != 1 {
		t.Fatalf("workingSubagents=%d, want only the subagent", lead.WorkingSubagents)
	}
	if got := plan.view.Counts["all"]; got != (SessionViewCount{Families: 2, Sessions: 6}) {
		t.Fatalf("counts=%+v, want the folded workers counted as sessions of one family", got)
	}
}

func TestAWorkerWhoseStarterIsArchivedMissingOrFilteredOutIsItsOwnRoot(t *testing.T) {
	f := newWorkerFixture()
	f.add("archived-lead", "", "main", func(r *dashboardSession) { r.Archived = true })
	f.add("of-archived", "archived-lead", "main")
	f.add("of-missing", "ghost", "main")
	f.add("claude-lead", "", "main", func(r *dashboardSession) { r.Surface = surface.KindClaude })
	f.add("codex-worker", "claude-lead", "main")
	f.add("unplaced-lead", "", "")
	f.add("of-unplaced", "unplaced-lead", "")
	plan := f.plan(SessionViewFilters{})
	for _, id := range []string{"of-archived", "of-missing", "of-unplaced"} {
		if family, member := familyOf(t, plan, id); family.RootID != id || member.Worker {
			t.Fatalf("%s is %+v under %s, want its own root", id, member, family.RootID)
		}
	}
	if family, _ := familyOf(t, plan, "codex-worker"); family.RootID != "claude-lead" {
		t.Fatalf("unfiltered, the worker is under %s, want claude-lead", family.RootID)
	}
	filtered := f.plan(SessionViewFilters{Surface: string(surface.KindCodex)})
	if family, member := familyOf(t, filtered, "codex-worker"); family.RootID != "codex-worker" || member.ContextOnly || len(family.Members) != 1 {
		t.Fatalf("filtered to codex, the worker is %+v, want its own root without its starter as context", family)
	}
}

func TestAStartedByCycleIsCutSoEverySessionIsListedOnce(t *testing.T) {
	f := newWorkerFixture()
	f.add("cycle-a", "cycle-c", "main")
	f.add("cycle-b", "cycle-a", "main")
	f.add("cycle-c", "cycle-b", "main")
	plan := f.plan(SessionViewFilters{})
	family, _ := familyOf(t, plan, "cycle-a")
	if family.RootID != "cycle-a" || len(family.Members) != 3 {
		t.Fatalf("the cycle became %+v, want one family rooted at the first session", plan.view.Families)
	}
	for _, id := range []string{"cycle-b", "cycle-c"} {
		familyOf(t, plan, id)
	}
}

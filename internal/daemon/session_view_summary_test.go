package daemon

import (
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAFamilyWaitingOnThePersonCountsOnlyAsNeedingThem(t *testing.T) {
	busy := dashboardSession{ID: "busy", Status: surface.StatusBusy}
	asking := dashboardSession{ID: "asking", Status: surface.StatusBusy, Attention: &SessionAttention{Kind: "ask"}}
	worker := dashboardSession{ID: "worker", Status: surface.StatusBusy}
	idle := dashboardSession{ID: "idle", Status: surface.StatusIdle, QueueCount: 2}
	plan := sessionViewPlan{rows: map[string]*dashboardSession{"busy": &busy, "asking": &asking, "worker": &worker, "idle": &idle}}
	summary := plan.summary([]SessionViewFamily{
		{RootID: "busy", Members: []SessionViewMember{{ID: "busy"}}},
		{RootID: "asking", Members: []SessionViewMember{{ID: "asking"}, {ID: "worker"}}},
		{RootID: "idle", Members: []SessionViewMember{{ID: "idle"}, {ID: "busy", ContextOnly: true}}},
	})
	if summary.WorkingFamilies != 1 || summary.AttentionFamilies != 1 || summary.QueuedMessages != 2 {
		t.Fatalf("summary=%+v, want 1 working, 1 needing the person (not also working) and 2 queued", summary)
	}
}

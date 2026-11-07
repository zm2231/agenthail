package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func goalRelayFixture(t *testing.T, once bool) (*Daemon, *registry.Registry, *goalRelaySurface, surface.Session, surface.Session) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, base, from, to := daemonFixture(t)
	base.caps = surface.Capabilities{Goal: true, Send: true}
	fake := newGoalRelaySurface(base, &surface.GoalState{Objective: "ship it", Status: surface.GoalStatusActive, CreatedAt: time.Now().Add(-time.Minute), UpdatedAt: time.Now().Add(-time.Minute)})
	d.Surfaces = []surface.Surface{fake}
	if err := r.PrepareRelaySource(context.Background(), fake, &from, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRelayRoute(from.ID, to.ID, ".*", once, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	return d, r, fake, from, to
}

func finishTurn(d *Daemon, fake *goalRelaySurface, from *surface.Session, id string) {
	fake.daemonSurface.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: &surface.ReplyResult{Text: id, Done: true}}
	d.syncRelays(context.Background(), fake, from)
}

func queuedTo(t *testing.T, r *registry.Registry, sessionID string) []registry.QueueRow {
	t.Helper()
	rows, err := r.ListQueue(true, nil)
	if err != nil {
		t.Fatal(err)
	}
	var to []registry.QueueRow
	for _, row := range rows {
		if row.SessionID == sessionID {
			to = append(to, row)
		}
	}
	return to
}

func TestABlockedGoalOnAGoalCompletedRouteIsReportedOnceWithHowToResume(t *testing.T) {
	d, r, fake, from, to := goalRelayFixture(t, false)
	fake.goal.Status, fake.goal.Reason, fake.goal.UpdatedAt = surface.GoalStatusBlocked, "needs the staging password", time.Now()
	finishTurn(d, fake, &from, "stuck")
	finishTurn(d, fake, &from, "still-stuck")
	reports := queuedTo(t, r, to.ID)
	if len(reports) != 1 {
		t.Fatalf("a blocked goal was reported %d times", len(reports))
	}
	for _, want := range []string{"is blocked", "needs the staging password", "agenthail goal from resume", "stuck"} {
		if !strings.Contains(reports[0].Message, want) {
			t.Fatalf("the report %q does not say %q", reports[0].Message, want)
		}
	}
}

func TestAGoalThatIsBlockedThenResumedAndCompletedReportsTheStopThenTheResultOnAOnceRoute(t *testing.T) {
	d, r, fake, from, to := goalRelayFixture(t, true)
	finishTurn(d, fake, &from, "working")
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusUsageLimited, time.Now()
	finishTurn(d, fake, &from, "limited")
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusActive, time.Now().Add(time.Second)
	finishTurn(d, fake, &from, "resumed")
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusComplete, time.Now().Add(2*time.Second)
	finishTurn(d, fake, &from, "done")
	finishTurn(d, fake, &from, "after")
	reports := queuedTo(t, r, to.ID)
	if len(reports) != 2 || !strings.Contains(reports[0].Message, "usage limit") || !strings.HasSuffix(reports[1].Message, "done") {
		t.Fatalf("reports=%+v", reports)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].Active {
		t.Fatalf("the once route did not end at completion: %+v err=%v", routes, err)
	}
}

func TestAGoalStoppedAgainAfterResumingIsReportedAgain(t *testing.T) {
	d, r, fake, from, to := goalRelayFixture(t, false)
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusBudgetLimited, time.Now()
	finishTurn(d, fake, &from, "first-stop")
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusActive, time.Now().Add(time.Second)
	finishTurn(d, fake, &from, "resumed")
	fake.goal.Status, fake.goal.UpdatedAt = surface.GoalStatusBudgetLimited, time.Now().Add(2*time.Second)
	finishTurn(d, fake, &from, "second-stop")
	reports := queuedTo(t, r, to.ID)
	if len(reports) != 2 || !strings.Contains(reports[1].Message, "agenthail goal from budget <tokens>") {
		t.Fatalf("reports=%+v", reports)
	}
}

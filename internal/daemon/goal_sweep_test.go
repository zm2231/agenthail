package daemon

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func goalSweepFixture(t *testing.T) (noticeFixture, *goalRelaySurface, *atomic.Int32) {
	t.Helper()
	f := newNoticeFixture(t)
	f.fake.caps = surface.Capabilities{Goal: true, Send: true}
	goals := &goalRelaySurface{daemonSurface: f.fake, goal: &surface.GoalState{Objective: "ship it", Status: surface.GoalStatusBlocked, Reason: "needs a token", CreatedAt: time.Now().Add(-time.Hour), UpdatedAt: time.Now()}}
	f.d.Surfaces = []surface.Surface{goals}
	f.d.discoverCatalog(context.Background())
	var desktop atomic.Int32
	f.d.notifyDesktop = func(_, _, sessionID, _, _ string, _ []surface.RequestAction) error {
		if sessionID == f.worker.ID {
			desktop.Add(1)
		}
		return nil
	}
	return f, goals, &desktop
}

func (f noticeFixture) touch(t *testing.T, at time.Time) {
	t.Helper()
	for _, id := range []string{"worker", "lead", "spawner"} {
		session := f.fake.sessions[id]
		session.LastActive = at
		f.fake.sessions[id] = session
		if err := f.reg.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
}

func (f noticeFixture) setWorkerStatus(t *testing.T, status surface.SessionStatus) {
	t.Helper()
	worker := f.fake.sessions["worker"]
	worker.Status = status
	f.fake.sessions["worker"] = worker
	if err := f.reg.RegisterSession(worker); err != nil {
		t.Fatal(err)
	}
	f.d.discoverCatalog(context.Background())
	sessions, err := f.reg.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range sessions {
		if session.ID == "worker" && session.Status != status {
			t.Fatalf("the worker lists as %s, not %s", session.Status, status)
		}
	}
}

func TestAWorkersBlockedGoalReachesItsLeadOnceAndThePerson(t *testing.T) {
	f, _, desktop := goalSweepFixture(t)
	f.setLead(t, "lead")
	now := time.Now()
	f.d.sweepStoppedGoals(context.Background(), now)
	f.touch(t, now.Add(time.Second))
	f.d.sweepStoppedGoals(context.Background(), now.Add(2*time.Second))
	notices := f.notices(t)
	if len(notices) != 1 || notices[0].SessionID != "lead" {
		t.Fatalf("notices=%+v", notices)
	}
	for _, want := range []string{"is blocked", "needs a token", "agenthail goal @builder resume"} {
		if !strings.Contains(notices[0].Message, want) {
			t.Fatalf("the notice %q does not say %q", notices[0].Message, want)
		}
	}
	if desktop.Load() != 1 {
		t.Fatalf("the person was notified %d times", desktop.Load())
	}
}

func TestAGoalReadThatFailsIsReadAgainOnTheNextSweep(t *testing.T) {
	f, goals, _ := goalSweepFixture(t)
	now := time.Now()
	goals.goalErr = errors.New("app-server unreachable")
	f.d.sweepStoppedGoals(context.Background(), now)
	goals.goalErr = nil
	f.d.sweepStoppedGoals(context.Background(), now.Add(goalSweepEvery))
	f.d.sweepStoppedGoals(context.Background(), now.Add(2*goalSweepEvery))
	if notices := f.notices(t); len(notices) != 1 {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestANoticeThatFailsToQueueIsQueuedOnTheNextSweep(t *testing.T) {
	f, _, _ := goalSweepFixture(t)
	loner := f.worker
	loner.ID = "loner"
	f.fake.sessions[loner.ID] = loner
	if err := f.reg.RegisterSession(loner); err != nil {
		t.Fatal(err)
	}
	if err := f.reg.RecordSessionOrigin(registry.OriginOf(loner.ID, "late-spawner")); err != nil {
		t.Fatal(err)
	}
	f.d.discoverCatalog(context.Background())
	var desktop atomic.Int32
	f.d.notifyDesktop = func(_, _, sessionID, _, _ string, _ []surface.RequestAction) error {
		if sessionID == loner.ID {
			desktop.Add(1)
		}
		return nil
	}
	f.worker = loner
	now := time.Now()
	f.d.sweepStoppedGoals(context.Background(), now)
	if notices := f.notices(t); len(notices) != 0 || desktop.Load() != 1 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
	spawner := f.fake.sessions["spawner"]
	spawner.ID = "late-spawner"
	if err := f.reg.RegisterSession(spawner); err != nil {
		t.Fatal(err)
	}
	f.d.sweepStoppedGoals(context.Background(), now.Add(goalSweepEvery))
	f.d.sweepStoppedGoals(context.Background(), now.Add(2*goalSweepEvery))
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "late-spawner" || desktop.Load() != 1 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
}

func TestARestartDoesNotRepeatAStoppedGoalToTheLeadOrThePerson(t *testing.T) {
	f, _, desktop := goalSweepFixture(t)
	f.setLead(t, "lead")
	f.d.sweepStoppedGoals(context.Background(), time.Now())
	restarted := newTestDaemon(t, f.reg, f.d.Surfaces)
	restarted.notifyDesktop = f.d.notifyDesktop
	restarted.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 1 || desktop.Load() != 1 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
}

func TestAStopWhoseNoticeWasQueuedButWhosePersonWasNotIsToldToThePersonAfterARestart(t *testing.T) {
	f, goals, desktop := goalSweepFixture(t)
	f.setLead(t, "lead")
	key := goalStopNoticeKey(f.worker.ID, goals.goal)
	restarted := newTestDaemon(t, f.reg, f.d.Surfaces)
	restarted.notifyDesktop = f.d.notifyDesktop
	f.d.notifyDesktop = func(string, string, string, string, string, []surface.RequestAction) error {
		panic("the daemon stopped before telling the person")
	}
	func() {
		defer func() { _ = recover() }()
		f.d.sweepStoppedGoals(context.Background(), time.Now())
	}()
	if used, err := f.reg.DeliveryKeyUsed(key); err != nil || used {
		t.Fatalf("the agent notice was queued before the person was told: used=%v err=%v", used, err)
	}
	restarted.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 1 || desktop.Load() != 1 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
}

func TestAStoppedGoalWithNoLeadGoesToTheSpawner(t *testing.T) {
	f, _, _ := goalSweepFixture(t)
	f.d.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 1 || notices[0].SessionID != "spawner" {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestAGoalStoppedAgainAfterResumingIsNoticedAgain(t *testing.T) {
	f, goals, desktop := goalSweepFixture(t)
	now := time.Now()
	f.d.sweepStoppedGoals(context.Background(), now)
	goals.goal.Status, goals.goal.UpdatedAt = surface.GoalStatusActive, now.Add(time.Second)
	f.touch(t, now.Add(time.Second))
	f.d.sweepStoppedGoals(context.Background(), now.Add(time.Second))
	goals.goal.Status, goals.goal.UpdatedAt = surface.GoalStatusUsageLimited, now.Add(2*time.Second)
	f.touch(t, now.Add(2*time.Second))
	f.d.sweepStoppedGoals(context.Background(), now.Add(2*time.Second))
	if notices := f.notices(t); len(notices) != 2 || !strings.Contains(notices[1].Message, "usage limit") {
		t.Fatalf("notices=%+v", notices)
	}
	if desktop.Load() != 2 {
		t.Fatalf("the person was notified %d times", desktop.Load())
	}
}

func TestAGoalARouteReportsOnIsLeftToTheRoute(t *testing.T) {
	f, _, desktop := goalSweepFixture(t)
	f.setLead(t, "lead")
	if _, err := f.reg.AddRelayRoute("worker", "lead", ".*", false, registry.HandoffGoalCompleted); err != nil {
		t.Fatal(err)
	}
	f.d.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 0 || desktop.Load() != 0 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
}

func TestAWorkingSessionIsAskedForItsGoalOnlyOnceItsTurnEnds(t *testing.T) {
	f, _, _ := goalSweepFixture(t)
	f.setWorkerStatus(t, surface.StatusBusy)
	f.d.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 0 {
		t.Fatalf("a working session was noticed: %+v", notices)
	}
	f.setWorkerStatus(t, surface.StatusIdle)
	f.d.sweepStoppedGoals(context.Background(), time.Now())
	if notices := f.notices(t); len(notices) != 1 {
		t.Fatalf("notices=%+v", notices)
	}
}

func TestASessionIdleForADayIsNotAskedForItsGoal(t *testing.T) {
	f, _, desktop := goalSweepFixture(t)
	f.d.sweepStoppedGoals(context.Background(), time.Now().Add(2*goalSweepWindow))
	if notices := f.notices(t); len(notices) != 0 || desktop.Load() != 0 {
		t.Fatalf("notices=%+v desktop=%d", notices, desktop.Load())
	}
}

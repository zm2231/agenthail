package daemon

import (
	"context"
	"net/http"
	"testing"
	"time"
)

func TestASendToAGoneLeadIsRefusedWhileTheDefaultRootCannotBeRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newCoordinatorFixture(t)
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{"codexRecentHours":9999}`})
	code, body := f.send(t, "lead-unreadable", goneCoordinator, "sent while the setting is broken")
	if code == http.StatusOK || f.started(t) != 0 || f.current(t) != goneCoordinator {
		t.Fatalf("the sender is told and no lead starts in the wrong root: status=%d body=%v started=%d current=%s", code, body, f.started(t), f.current(t))
	}
}

func TestAQueuedMessageForAGoneLeadWaitsWhileTheDefaultRootCannotBeRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newCoordinatorFixture(t)
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{"codexRecentHours":9999}`})
	if err := f.r.QueueMessage(goneCoordinator, "held for the coordinator"); err != nil {
		t.Fatal(err)
	}
	session, err := f.r.Session(goneCoordinator)
	if err != nil {
		t.Fatal(err)
	}
	attempt := func(now time.Time) {
		t.Helper()
		item, err := f.r.ClaimNextMessage(goneCoordinator, now)
		if err != nil || item == nil {
			t.Fatalf("the message is no longer claimable at %s: item=%+v err=%v", now, item, err)
		}
		if !f.daemon.restartCoordinator(context.Background(), session, item, now) {
			t.Fatal("the restart did not handle the message")
		}
	}
	now := time.Now()
	for range maxDeliveryAttempts + 3 {
		now = now.Add(30 * 24 * time.Hour)
		attempt(now)
	}
	if started := f.started(t); started != 0 {
		t.Fatalf("a lead started without its root: %d", started)
	}
	rows, err := f.r.ListQueue(false, f.daemon.adapters())
	if err != nil || len(rows) != 1 || rows[0].Status != "pending" || rows[0].Attempts != 0 {
		t.Fatalf("the message must wait, not spend its attempts: rows=%+v err=%v", rows, err)
	}
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{}`})
	attempt(now.Add(30 * 24 * time.Hour))
	if current := f.current(t); current == goneCoordinator || f.started(t) != 1 {
		t.Fatalf("the message starts the lead once the setting reads: current=%s started=%d", current, f.started(t))
	}
}

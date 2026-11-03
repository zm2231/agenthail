package daemon

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const brokenSettings = `{not json`

func TestASendToAGoneLeadQueuesUntilTheSettingsFileIsFixedThenDelivers(t *testing.T) {
	for _, test := range []struct{ name, content, says string }{
		{"malformed", brokenSettings, "parse dashboard config"},
		{"invalid value", `{"codexRecentHours":9999}`, "Codex recent hours"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			f := newCoordinatorFixture(t)
			writeUsageFixture(t, map[string]string{DashboardConfigPath(): test.content})
			code, body := f.send(t, "lead-unreadable", goneCoordinator, "sent while the setting is broken")
			detail := receiptField(body, "detail")
			if code != http.StatusOK || !strings.HasPrefix(detail, "Queued for") || !strings.Contains(detail, DashboardConfigPath()) || !strings.Contains(detail, test.says) {
				t.Fatalf("the send says it is queued and why: status=%d body=%v", code, body)
			}
			f.daemon.scanAndRelay(context.Background())
			if f.r.QueueCount(goneCoordinator) != 1 || f.started(t) != 0 || f.current(t) != goneCoordinator {
				t.Fatalf("the message waits and no lead starts: queued=%d started=%d current=%s", f.r.QueueCount(goneCoordinator), f.started(t), f.current(t))
			}
			writeUsageFixture(t, map[string]string{DashboardConfigPath(): `{}`})
			rows, err := f.r.ListQueue(false, f.daemon.adapters())
			if err != nil || len(rows) != 1 || rows[0].Status != "pending" || rows[0].Attempts != 0 {
				t.Fatalf("waiting spends no attempts: rows=%+v err=%v", rows, err)
			}
			session, err := f.r.Session(goneCoordinator)
			if err != nil {
				t.Fatal(err)
			}
			item, err := f.r.ClaimNextMessage(goneCoordinator, time.Now().Add(30*24*time.Hour))
			if err != nil || item == nil {
				t.Fatalf("item=%+v err=%v", item, err)
			}
			if !f.daemon.restartCoordinator(context.Background(), session, item, time.Now()) {
				t.Fatal("the restart did not handle the message")
			}
			current := f.current(t)
			if current == goneCoordinator || f.started(t) != 1 {
				t.Fatalf("the fixed file lets the lead start: current=%s started=%d", current, f.started(t))
			}
			f.landed(t, current, "sent while the setting is broken")
		})
	}
}

func TestAChannelMessageToAGoneLeadQueuesWhileTheSettingsFileCannotBeRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newCoordinatorFixture(t)
	if _, err := f.r.CreateChannel("team"); err != nil {
		t.Fatal(err)
	}
	if err := f.r.AddToChannel("team", goneCoordinator); err != nil {
		t.Fatal(err)
	}
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): brokenSettings})
	code, body := f.channelSend(t, "channel-unreadable", "team", "", "standup")
	if code != http.StatusOK || !strings.Contains(body, broadcast.OutcomeQueued) || f.started(t) != 0 || f.r.QueueCount(goneCoordinator) != 1 {
		t.Fatalf("status=%d body=%s started=%d queued=%d", code, body, f.started(t), f.r.QueueCount(goneCoordinator))
	}
}

func TestSendsThatCannotQueueAreRefusedWhileTheSettingsFileCannotBeRead(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newCoordinatorFixture(t)
	writeUsageFixture(t, map[string]string{DashboardConfigPath(): brokenSettings})
	code, body := f.sendToLead(t, "lead-no-queue", map[string]any{"message": "urgent", "noQueue": true})
	if code == http.StatusOK || !strings.Contains(fmt.Sprint(body), DashboardConfigPath()) || f.started(t) != 0 || f.r.QueueCount(goneCoordinator) != 0 {
		t.Fatalf("a no-queue send is refused: status=%d body=%v started=%d queued=%d", code, body, f.started(t), f.r.QueueCount(goneCoordinator))
	}
	if _, err := f.r.SetCoordinator(registry.CoordinatorSpec{ProjectID: "project-fixture", ProjectRoot: f.host.Cwd, Name: "fixture-lead", Surface: string(surface.KindClaude), Cwd: f.host.Cwd}, registry.CoordinatorChange{Replace: true}); err != nil {
		t.Fatal(err)
	}
	code, body = f.sendToLead(t, "lead-first", map[string]any{"message": "first task"})
	if code == http.StatusOK || !strings.Contains(fmt.Sprint(body), DashboardConfigPath()) || f.started(t) != 0 {
		t.Fatalf("a lead with no conversation has nowhere to hold the message: status=%d body=%v started=%d", code, body, f.started(t))
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

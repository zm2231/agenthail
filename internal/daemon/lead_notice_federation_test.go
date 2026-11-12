package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAPromptReachesAPairedHostsLeadOnceAcrossARestart(t *testing.T) {
	local, remote, _ := pairedFederation(t)
	localRepo := widgetRepo(t, "https://github.com/example/widget.git")
	remoteRepo := widgetRepo(t, "https://github.com/example/widget.git")
	moveSession(t, local, "local-a", localRepo, time.Now())
	moveSession(t, remote, "remote-a", remoteRepo, time.Now())
	if _, err := remote.reg.SetCoordinator(registry.CoordinatorSpec{ProjectID: "repo:github.com/example/widget", ProjectRoot: remoteRepo, Name: "vps-lead", Surface: "codex", Cwd: remoteRepo}, registry.CoordinatorChange{SessionID: "remote-a", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	remote.daemon.discoverCatalog(context.Background())
	local.daemon.discoverCatalog(context.Background())
	testkit.Eventually(t, "the remote coordinator mirrored with its session", 10*time.Second, func() bool {
		specs, _ := local.reg.ProjectCoordinators("repo:github.com/example/widget")
		return len(specs) == 1 && specs[0].HostID != "" && local.daemon.coordinator().LeadSession(specs[0]) != ""
	})
	worker, err := local.reg.Session("local-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := local.reg.RecordSessionOrigin(registry.OriginOf("local-a", "remote-a")); err != nil {
		t.Fatal(err)
	}
	waiting := surface.Waiting{Kind: surface.WaitingApproval, ID: "approve-remote", Answerable: true}
	local.daemon.syncWaits(*worker, &waiting)
	local.daemon.background.wait()
	open, err := local.reg.OpenPermissionAsks("local-a")
	if err != nil || len(open) != 1 || open[0].RoutedTo == registry.AskToPerson {
		t.Fatalf("open=%+v err=%v, want the ask routed to the paired host's lead", open, err)
	}
	restarted := newTestDaemon(t, local.reg, local.daemon.Surfaces)
	restarted.tellPermissionAsks(open)
	restarted.background.wait()
	askID := open[0].ID
	notices := func() int {
		remote.fake.mu.Lock()
		defer remote.fake.mu.Unlock()
		count := 0
		for _, message := range remote.fake.messages {
			if strings.Contains(message, askID) {
				count++
			}
		}
		return count
	}
	held := func() []registry.QueueRow {
		rows, err := local.reg.ListQueue(true, nil)
		if err != nil {
			t.Fatal(err)
		}
		var mine []registry.QueueRow
		for _, row := range rows {
			if strings.Contains(row.Message, askID) {
				mine = append(mine, row)
			}
		}
		return mine
	}
	testkit.Eventually(t, "the held notice delivered to the paired host's lead", 10*time.Second, func() bool {
		rows := held()
		return len(rows) == 1 && rows[0].Status == "delivered" && notices() >= 1
	})
	if rows, got := held(), notices(); len(rows) != 1 || got != 1 {
		t.Fatalf("held=%+v delivered to the paired host's lead %d times", rows, got)
	}
}

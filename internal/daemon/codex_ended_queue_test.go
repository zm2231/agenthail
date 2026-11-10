package daemon

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestAMessageQueuedForAnAppServerThreadWhoseRuntimeStoppedStartsItAndIsDeliveredOnce(t *testing.T) {
	const thread = "019f004a-0000-7000-8000-0000000e0001"
	lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
	lifecycle.Start(t).AddThread(codextest.Thread{ID: thread, Originator: "agenthail", Source: "agenthail"})
	lifecycle.Crash()
	_, r, _, _, _ := daemonFixture(t)
	if err := r.RegisterSession(surface.Session{ID: thread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: surface.StatusOffline, Source: "agenthail", Transport: "managed"}); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage(thread, "deliver once Codex is back"); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, []surface.Surface{surfaces.NewCodex(unreachableDesktop)})
	for range 2 {
		d.scanAndRelay(context.Background())
	}
	daemon := lifecycle.Daemon()
	var delivered int
	for _, call := range daemon.Calls("turn/start") {
		if strings.Contains(fmt.Sprint(call), "deliver once Codex is back") {
			delivered++
		}
	}
	if delivered != 1 || r.QueueCount(thread) != 0 {
		t.Fatalf("delivered %d times, %d still queued; app-server methods=%v lifecycle=%v", delivered, r.QueueCount(thread), daemon.Methods(), lifecycle.Commands())
	}
}

func TestAMessageQueuedForAnAppServerThreadThatIsNotLoadedLoadsItAndIsDeliveredOnce(t *testing.T) {
	for i, status := range []surface.SessionStatus{surface.StatusNotLoaded, surface.StatusOffline} {
		t.Run(string(status), func(t *testing.T) {
			thread := fmt.Sprintf("019f004a-0000-7000-8000-0000000e001%d", i)
			lifecycle := codextest.InstallLifecycle(t, codextest.Home(t), nil)
			lifecycle.Start(t).AddThread(codextest.Thread{ID: thread, Originator: "agenthail", Source: "agenthail"})
			_, r, _, _, _ := daemonFixture(t)
			if err := r.RegisterSession(surface.Session{ID: thread, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer, Status: status, Source: "agenthail", Transport: "managed"}); err != nil {
				t.Fatal(err)
			}
			if err := r.QueueMessage(thread, "deliver once it is loaded"); err != nil {
				t.Fatal(err)
			}
			d := newTestDaemon(t, r, []surface.Surface{surfaces.NewCodex(unreachableDesktop)})
			for range 2 {
				d.scanAndRelay(context.Background())
			}
			daemon := lifecycle.Daemon()
			var delivered int
			for _, call := range daemon.Calls("turn/start") {
				if strings.Contains(fmt.Sprint(call), "deliver once it is loaded") {
					delivered++
				}
			}
			if delivered != 1 || r.QueueCount(thread) != 0 {
				t.Fatalf("delivered %d times, %d still queued; app-server methods=%v", delivered, r.QueueCount(thread), daemon.Methods())
			}
		})
	}
}

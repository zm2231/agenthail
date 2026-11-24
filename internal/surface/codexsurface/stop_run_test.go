package codexsurface

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestStopRunInterruptsAManagedThreadAndDropsAgenthailsSubscriptions(t *testing.T) {
	fake := startManagedCodex(t, nil)
	fake.AddThread(codextest.Thread{ID: managedThreadID, Originator: "agenthail", Source: "agenthail", Turns: []codextest.Turn{{ID: "turn-1", Status: "inProgress"}}})
	fake.Load(managedThreadID)
	codex := isolatedManagedRuntime(t)
	ctx := context.Background()
	session, err := codex.Resolve(ctx, managedThreadID)
	if err != nil {
		t.Fatal(err)
	}
	follower, err := codex.dialManaged(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer follower.Close()
	if !codex.subscribeManagedThread(ctx, follower, managedThreadID) || fake.Subscribers(managedThreadID) == 0 {
		t.Fatalf("subscribers=%d, want a follower before the stop", fake.Subscribers(managedThreadID))
	}
	run, err := codex.StopRun(ctx, session)
	if err != nil || !run.Stopped {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	interrupts := fake.Calls("turn/interrupt")
	if len(interrupts) != 1 || interrupts[0]["turnId"] != "turn-1" {
		t.Fatalf("interrupts=%v, want turn-1 interrupted", interrupts)
	}
	if n := fake.Subscribers(managedThreadID); n != 0 {
		t.Fatalf("subscribers=%d after the stop, want none so Codex can unload the thread", n)
	}
	if run, err := codex.StopRun(ctx, session); err != nil || run != (surface.RunStop{}) {
		t.Fatalf("second stop run=%+v err=%v, want nothing left to stop", run, err)
	}
}

func TestAWatchSubscribeQueuedBehindAStopLeavesNoSubscriptionOnTheThread(t *testing.T) {
	fastTerminalPolls(t)
	stopping, proceed := make(chan struct{}), make(chan struct{})
	var interrupted atomic.Bool
	var once sync.Once
	daemon := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "turn/interrupt" {
			interrupted.Store(true)
		}
		if method == "thread/unsubscribe" && interrupted.Load() {
			once.Do(func() { close(stopping); <-proceed })
		}
		return nil
	})
	daemon.AddThread(runningThread(managedThreadID))
	daemon.Load(managedThreadID)
	daemon.HoldElsewhere(managedThreadID, true)
	codex := watchedCodex(t, managedThreadID)
	done := make(chan error, 1)
	go func() {
		_, err := codex.StopRun(context.Background(), &surface.Session{ID: managedThreadID})
		done <- err
	}()
	<-stopping
	resumes := resumesOf(daemon, managedThreadID)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": waitingStatus("waitingOnApproval")})
	time.Sleep(200 * time.Millisecond)
	daemon.Notify("thread/status/changed", map[string]any{"threadId": managedThreadID, "status": map[string]any{"type": "idle"}})
	close(proceed)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the queued watch to subscribe after the stop", 5*time.Second, func() bool {
		return resumesOf(daemon, managedThreadID) > resumes
	})
	testkit.Eventually(t, "no Agenthail subscription left on the stopped thread", 5*time.Second, func() bool {
		return daemon.Subscribers(managedThreadID) == 0
	})
}

package codexsurface

import (
	"context"
	"testing"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/surface"
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

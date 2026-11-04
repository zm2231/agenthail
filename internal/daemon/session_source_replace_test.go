package daemon

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

func TestAReplacementSourceStartsOnlyAfterTheStoppedOneHasFinished(t *testing.T) {
	_, reg, fake, from, _ := daemonFixture(t)
	manager := newTestSourceManager(t, reg)
	late := make(chan struct{})
	var streaming, most atomic.Int32
	adapter := &scriptedStreamSurface{flakySeedSurface: &flakySeedSurface{daemonSurface: fake}}
	adapter.caps.Stream = true
	adapter.script = func(call int32, ctx context.Context, onEvent func(surface.StreamEvent)) error {
		now := streaming.Add(1)
		defer streaming.Add(-1)
		for seen := most.Load(); now > seen && !most.CompareAndSwap(seen, now); seen = most.Load() {
		}
		<-ctx.Done()
		if call == 1 {
			<-late
			onEvent(turnText("turn-old", "written by the stopped source"))
		}
		return ctx.Err()
	}

	release, err := manager.hold(&from, adapter, "first")
	if err != nil {
		t.Fatal(err)
	}
	testkit.Eventually(t, "the first source streams", 5*time.Second, func() bool { return adapter.calls.Load() == 1 })
	release()

	held := make(chan func(), 1)
	go func() {
		next, err := manager.hold(&from, adapter, "second")
		if err != nil {
			t.Error(err)
			next = func() {}
		}
		held <- next
	}()
	select {
	case next := <-held:
		t.Cleanup(next)
		close(late)
		t.Fatal("a replacement source was held while the stopped one was still streaming")
	case <-time.After(300 * time.Millisecond):
	}
	close(late)
	select {
	case next := <-held:
		t.Cleanup(next)
	case <-time.After(5 * time.Second):
		t.Fatal("the replacement source was never held after the stopped one finished")
	}
	testkit.Eventually(t, "the replacement streams", 5*time.Second, func() bool { return adapter.calls.Load() == 2 })
	if got := most.Load(); got != 1 {
		t.Fatalf("%d streams of one session ran at once", got)
	}
}

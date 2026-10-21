package daemon

import (
	"context"
	"testing"
	"time"
)

func TestBackgroundWorkStopCancelsAndWaitsForRunningWork(t *testing.T) {
	work := newBackgroundWork()
	running, finished := make(chan struct{}), make(chan struct{})
	if !work.start(func(ctx context.Context) {
		close(running)
		<-ctx.Done()
		time.Sleep(50 * time.Millisecond)
		close(finished)
	}) {
		t.Fatal("work refused before the stop")
	}
	<-running
	work.stop()
	select {
	case <-finished:
	default:
		t.Fatal("stop returned before the work it cancelled finished")
	}
	if work.start(func(context.Context) { t.Error("work ran after the stop") }) {
		t.Fatal("work accepted after the stop")
	}
}

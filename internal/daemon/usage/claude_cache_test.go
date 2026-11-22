package usage

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestAScopedWeeklyMeterIsLabelledByItsModel(t *testing.T) {
	now := time.Now()
	reading := &surface.Usage{FetchedAt: now, Limits: []surface.UsageWindow{{Kind: "weekly_scoped", Scope: "Fable", Percent: 40}, {Kind: "weekly_scoped", Percent: 10}}}
	meters := Meters("claude", reading, now)
	if len(meters) != 2 || meters[0].Label != "Claude week, Fable" || meters[0].Scope != "Fable" || meters[1].Label != "Claude week, one model" {
		t.Fatalf("scoped meters: %+v", meters)
	}
}

func TestACancelledPollDoesNotFailTheReadAnotherPollWaitsOn(t *testing.T) {
	reader := &blockingRootReader{started: make(chan struct{}), release: make(chan struct{})}
	root := surface.ClaudeConfigRoot{Root: "/r", SignedInAs: &surface.ClaudeLogin{AccountID: "a"}}
	var cache ClaudeCache
	now := time.Now()
	first, cancel := context.WithCancel(context.Background())
	firstDone := make(chan error, 1)
	go func() {
		_, _, _, err := cache.Read(first, reader, root, now)
		firstDone <- err
	}()
	<-reader.started
	secondDone := make(chan *surface.Usage, 1)
	go func() {
		_, usage, _, _ := cache.Read(context.Background(), reader, root, now)
		secondDone <- usage
	}()
	cancel()
	close(reader.release)
	if err := <-firstDone; err != nil {
		t.Fatalf("the read outlives the poll that started it: %v", err)
	}
	if usage := <-secondDone; usage == nil || len(usage.Limits) != 1 {
		t.Fatalf("the waiting poll gets the finished reading: %+v", usage)
	}
	if got := reader.calls.Load(); got != 1 {
		t.Fatalf("claude.ai read %d times, want 1", got)
	}
}

func TestABurstOfStartsRefreshesAStaleReadingOnce(t *testing.T) {
	var cache ClaudeCache
	reader := &blockingRootReader{started: make(chan struct{}), release: make(chan struct{})}
	root := surface.ClaudeConfigRoot{Root: "/r", SignedInAs: &surface.ClaudeLogin{AccountID: "a"}}
	before := runtime.NumGoroutine()
	for range 100 {
		if usage := cache.Cached(reader, root, time.Now()); usage != nil {
			t.Fatalf("a cold cache has no reading yet: %+v", usage)
		}
	}
	<-reader.started
	if grew := runtime.NumGoroutine() - before; grew > 3 {
		t.Fatalf("100 starts queued %d refreshes behind one claude.ai read", grew)
	}
	close(reader.release)
	deadline := time.Now().Add(5 * time.Second)
	for cache.entry(root.Root).refreshing.Load() {
		if time.Now().After(deadline) {
			t.Fatal("the refresh did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if usage := cache.Cached(reader, root, time.Now()); usage == nil || reader.calls.Load() != 1 {
		t.Fatalf("the next start reads the refreshed snapshot: usage=%+v reads=%d", usage, reader.calls.Load())
	}
}

type blockingRootReader struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (r *blockingRootReader) ClaudeConfigRoots(context.Context) ([]surface.ClaudeConfigRoot, error) {
	return nil, nil
}

func (r *blockingRootReader) ClaudeConfigRoot(ctx context.Context, path string) (surface.ClaudeConfigRoot, error) {
	if r.calls.Add(1) == 1 {
		close(r.started)
	}
	<-r.release
	if err := ctx.Err(); err != nil {
		return surface.ClaudeConfigRoot{}, err
	}
	return surface.ClaudeConfigRoot{Root: path, Usage: &surface.Usage{Limits: []surface.UsageWindow{{Kind: "session", Percent: 30}}}}, nil
}

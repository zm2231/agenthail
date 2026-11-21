package daemon

import (
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type gatedCapabilities struct {
	*daemonSurface
	calls   atomic.Int32
	release chan struct{}
}

func (g *gatedCapabilities) Capabilities() surface.Capabilities {
	g.calls.Add(1)
	<-g.release
	return g.daemonSurface.Capabilities()
}

func TestConcurrentSnapshotReadsShareOneRebuild(t *testing.T) {
	d, _, fake, dashboard, handler, _ := baseFixture(t)
	gated := &gatedCapabilities{daemonSurface: fake, release: make(chan struct{})}
	d.Surfaces = []surface.Surface{gated}
	close(gated.release)
	if code := getSnapshot(t, handler, "/api/v1/projects/activity", "").Code; code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	perBuild := gated.calls.Swap(0)
	gated.release = make(chan struct{})
	dashboard.invalidate()
	const readers = 6
	var wg sync.WaitGroup
	codes := make([]int, readers)
	for i := range readers {
		wg.Go(func() { codes[i] = getSnapshot(t, handler, "/api/v1/projects/activity", "").Code })
	}
	for deadline := time.Now().Add(time.Second); gated.calls.Load() < readers && time.Now().Before(deadline); {
		time.Sleep(5 * time.Millisecond)
	}
	close(gated.release)
	wg.Wait()
	for i, code := range codes {
		if code != http.StatusOK {
			t.Fatalf("reader %d: status %d", i, code)
		}
	}
	if calls := gated.calls.Load(); calls != perBuild {
		t.Fatalf("%d concurrent reads asked the surface %d times, one build asks it %d times", readers, calls, perBuild)
	}
}

type firstBuildHeld struct {
	*daemonSurface
	calls   atomic.Int32
	held    chan struct{}
	release chan struct{}
}

func (f *firstBuildHeld) Capabilities() surface.Capabilities {
	if f.calls.Add(1) == 1 {
		close(f.held)
		<-f.release
	}
	return f.daemonSurface.Capabilities()
}

func TestAnOlderRebuildFinishingLastKeepsTheNewerState(t *testing.T) {
	d, _, fake, dashboard, handler, _ := baseFixture(t)
	held := &firstBuildHeld{daemonSurface: fake, held: make(chan struct{}), release: make(chan struct{})}
	d.Surfaces = []surface.Surface{held}
	older := make(chan int, 1)
	go func() { older <- getSnapshot(t, handler, "/api/v1/snapshot", "").Code }()
	<-held.held
	dashboard.invalidate()
	newer := getSnapshot(t, handler, "/api/v1/snapshot", "")
	tag := newer.Header().Get("ETag")
	if newer.Code != http.StatusOK || tag == "" {
		t.Fatalf("newer read: status=%d etag=%q", newer.Code, tag)
	}
	close(held.release)
	if code := <-older; code != http.StatusOK {
		t.Fatalf("older read: status %d", code)
	}
	calls := held.calls.Load()
	if again := getSnapshot(t, handler, "/api/v1/snapshot", tag); again.Code != http.StatusNotModified || held.calls.Load() != calls {
		t.Fatalf("after the older build finished: status=%d etag=%q, want the newer state %q served from the cache", again.Code, again.Header().Get("ETag"), tag)
	}
}

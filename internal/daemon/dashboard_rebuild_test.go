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

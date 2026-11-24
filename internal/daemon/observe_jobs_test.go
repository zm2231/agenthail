package daemon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// gatedSurface holds each observation of a gated session until the test lets
// it go, and records when every other session was observed.
type gatedSurface struct {
	*daemonSurface
	gated    map[string]bool
	release  chan struct{}
	mu       sync.Mutex
	observed map[string]time.Time
	calls    map[string]int
	running  atomic.Int32
	peak     atomic.Int32
}

func newGatedSurface(base *daemonSurface, gated ...string) *gatedSurface {
	s := &gatedSurface{daemonSurface: base, gated: map[string]bool{}, release: make(chan struct{}), observed: map[string]time.Time{}, calls: map[string]int{}}
	for _, id := range gated {
		s.gated[id] = true
	}
	return s
}

func (s *gatedSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	running := s.running.Add(1)
	defer s.running.Add(-1)
	for peak := s.peak.Load(); running > peak && !s.peak.CompareAndSwap(peak, running); peak = s.peak.Load() {
	}
	s.mu.Lock()
	s.calls[session.ID]++
	s.mu.Unlock()
	if s.gated[session.ID] {
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s.mu.Lock()
	s.observed[session.ID] = time.Now()
	s.mu.Unlock()
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}

func (s *gatedSurface) observedAt(id string) time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.observed[id]
}

func (s *gatedSurface) callsOf(id string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[id]
}

func watchSessions(t *testing.T, r *registry.Registry, fake *daemonSurface, kind surface.SurfaceKind, ids ...string) {
	t.Helper()
	for _, id := range ids {
		session := surface.Session{ID: id, Surface: kind, Status: surface.StatusIdle}
		if kind == surface.KindCodex {
			session.RuntimeKind, session.Source, session.Transport = surface.RuntimeCodexDesktop, "vscode", "desktop"
		}
		if err := r.RecordObservedSession(session); err != nil {
			t.Fatal(err)
		}
		fake.sessions[id] = session
		if err := r.QueueMessage(id, "pending work for "+id); err != nil {
			t.Fatal(err)
		}
	}
}

// A provider call that takes a while held up every other watched session's
// observation in the same scan: a ready session waited for each slow one
// before it, about 250 ms behind two slow sessions.
func TestASlowObservationDoesNotDelayAnotherSessions(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake, "slow-1", "slow-2")
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, "slow-1", "slow-2", "ready")
	started := time.Now()
	finished := d.planScan(t.Context())
	deadline := time.After(5 * time.Second)
	for gated.observedAt("ready").IsZero() {
		select {
		case <-deadline:
			t.Fatal("the ready session was not observed while the slow ones were")
		case <-time.After(time.Millisecond):
		}
	}
	if wait := gated.observedAt("ready").Sub(started); wait > 100*time.Millisecond {
		t.Fatalf("the ready session was observed %s after the scan began, want it within 100ms", wait)
	}
	close(gated.release)
	<-finished
}

func TestAScanDoesNotObserveASessionWhoseLastObservationIsStillRunning(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake, "slow")
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, "slow", "ready")
	first := d.planScan(t.Context())
	for gated.callsOf("slow") == 0 {
		time.Sleep(time.Millisecond)
	}
	watched, err := r.WatchedSessions()
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		<-d.planScan(t.Context())
		if pending := d.observeJobs.sessions(); pending > len(watched) {
			t.Fatalf("%d sessions have jobs, more than the %d watched", pending, len(watched))
		}
	}
	if calls := gated.callsOf("slow"); calls != 1 {
		t.Fatalf("the slow session was observed %d times while its first observation ran, want once", calls)
	}
	close(gated.release)
	<-first
	<-d.planScan(t.Context())
	if calls := gated.callsOf("slow"); calls != 2 {
		t.Fatalf("the slow session was observed %d times, want once more after its first observation ended", calls)
	}
}

func TestEachSurfaceRunsAtMostItsShareOfObservationsAtOnce(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake)
	ids := make([]string, 3*observeJobsPerSurface)
	for index := range ids {
		ids[index] = fmt.Sprintf("codex-%d", index)
		gated.gated[ids[index]] = true
	}
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, ids...)
	finished := d.planScan(t.Context())
	for gated.running.Load() < observeJobsPerSurface {
		time.Sleep(time.Millisecond)
	}
	time.Sleep(20 * time.Millisecond)
	if running := gated.running.Load(); running != observeJobsPerSurface {
		t.Fatalf("%d observations of one surface run at once, want %d", running, observeJobsPerSurface)
	}
	close(gated.release)
	<-finished
	if peak := gated.peak.Load(); peak > observeJobsPerSurface {
		t.Fatalf("at most %d observations of one surface ran at once, want no more than %d", peak, observeJobsPerSurface)
	}
	for _, id := range ids {
		if gated.callsOf(id) != 1 {
			t.Fatalf("%s was observed %d times, want once", id, gated.callsOf(id))
		}
	}
}

func TestAQueuedMessageIsDeliveredOnceWhileScansOverlap(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake, "slow")
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, "slow", "target")
	var scans sync.WaitGroup
	for range 4 {
		scans.Add(1)
		go func() {
			defer scans.Done()
			d.planScan(t.Context())
		}()
	}
	scans.Wait()
	close(gated.release)
	<-d.planScan(t.Context())
	d.observeJobs.wait()
	sent := 0
	for _, message := range fake.sentMessages() {
		if strings.Contains(message, "pending work for target") {
			sent++
		}
	}
	if sent != 1 {
		t.Fatalf("the queued message was sent %d times, want once", sent)
	}
}

func TestStoppingTheDaemonLeavesNoObservationRunning(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake, "slow")
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, "slow")
	ctx, cancel := context.WithCancel(t.Context())
	finished := d.planScan(ctx)
	for gated.callsOf("slow") == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	d.observeJobs.wait()
	select {
	case <-finished:
	default:
		t.Fatal("a scan was left unfinished after its observations stopped")
	}
	if running := gated.running.Load(); running != 0 {
		t.Fatalf("%d observations still run after the daemon stopped", running)
	}
	if pending := d.observeJobs.sessions(); pending != 0 {
		t.Fatalf("%d sessions still hold a job after the daemon stopped", pending)
	}
}

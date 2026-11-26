package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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

func (q *observeJobQueue) holds(session string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	_, found := q.pending[session]
	return found
}

func waitUntil(t *testing.T, what string, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !done() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(time.Millisecond)
	}
}

func watchSessions(t *testing.T, r *registry.Registry, fake *daemonSurface, kind surface.SurfaceKind, ids ...string) {
	t.Helper()
	for _, id := range ids {
		session := surface.Session{ID: id, Surface: kind, Status: surface.StatusIdle}
		switch kind {
		case surface.KindCodex:
			session.RuntimeKind, session.Source, session.Transport = surface.RuntimeCodexDesktop, "vscode", "desktop"
		case surface.KindNotion:
			session.RuntimeKind = surface.RuntimeNotionWeb
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
	// Watched sessions are scanned in id order, so a serial scan reaches
	// "unblocked" only after both slow sessions.
	watchSessions(t, r, fake, surface.KindCodex, "slow-1", "slow-2", "unblocked")
	finished := d.planScan(t.Context())
	waitUntil(t, "the unblocked session is observed while both slow ones are still held", func() bool { return !gated.observedAt("unblocked").IsZero() })
	close(gated.release)
	<-finished
}

func TestAScanDoesNotObserveASessionWhoseLastObservationIsStillRunning(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	gated := newGatedSurface(fake, "slow")
	d.Surfaces = []surface.Surface{gated}
	watchSessions(t, r, fake, surface.KindCodex, "slow", "ready")
	first := d.planScan(t.Context())
	waitUntil(t, "the slow session is being observed", func() bool { return gated.callsOf("slow") > 0 })
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
	waitUntil(t, "a surface's share of observations runs", func() bool { return gated.running.Load() >= observeJobsPerSurface })
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
	waitUntil(t, "the slow session is being observed", func() bool { return gated.callsOf("slow") > 0 })
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

// writingSurface writes a journal row each time it observes a session and
// reports a newly finished turn, so every job writes runtime state, a turn
// completion and the journal while the others do.
type writingSurface struct {
	*gatedSurface
	r *registry.Registry
}

func (s *writingSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	if _, err := s.gatedSurface.Observe(ctx, session); err != nil {
		return nil, err
	}
	turn := "turn-0"
	entry, err := sessionJournalEntry(session.ID, sessionJournalPayload{ItemID: turn, ProviderKey: turn, Op: "upsert", Kind: "text", Role: "assistant", Body: "finished " + turn, TS: time.Now().UTC().Format(time.RFC3339Nano)})
	if err != nil {
		return nil, err
	}
	if _, _, err := s.r.AppendSessionJournalEntry(entry, registry.SessionJournalRetention{Count: sessionJournalRetentionCount, Bytes: sessionJournalRetentionBytes}); err != nil {
		return nil, err
	}
	return &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: turn, CompletedAt: time.Now(), Reply: &surface.ReplyResult{Done: true, Text: "finished " + turn}}, nil
}

func TestObservationsRunningAtOnceAllWriteTheirState(t *testing.T) {
	d, r, codex, _, _ := daemonFixture(t)
	notion := &daemonSurface{kind: surface.KindNotion, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	var surfaces []surface.Surface
	var ids []string
	for _, fake := range []*daemonSurface{codex, notion} {
		gated := newGatedSurface(fake)
		writing := &writingSurface{gatedSurface: gated, r: r}
		surfaces = append(surfaces, writing)
		for index := range 2 * observeJobsPerSurface {
			id := fmt.Sprintf("%s-%d", fake.Name(), index)
			ids = append(ids, id)
			gated.gated[id] = true
			watchSessions(t, r, fake, fake.Name(), id)
			if err := r.SaveRuntimeState(id, surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-earlier"}); err != nil {
				t.Fatal(err)
			}
		}
	}
	d.Surfaces = surfaces
	finished := d.planScan(t.Context())
	waitUntil(t, "every observation slot is in use", func() bool {
		return surfaces[0].(*writingSurface).running.Load()+surfaces[1].(*writingSurface).running.Load() >= observeJobs
	})
	for _, each := range surfaces {
		close(each.(*writingSurface).release)
	}
	<-finished
	for _, id := range ids {
		state, found, err := r.RuntimeState(id)
		if err != nil || !found || state.CompletedTurnID != "turn-0" {
			t.Fatalf("%s runtime state=%+v found=%v err=%v, want its finished turn saved", id, state, found, err)
		}
		if _, found, err := r.SessionJournalEntryByProviderKey(id, "turn-0"); err != nil || !found {
			t.Fatalf("%s journal row found=%v err=%v", id, found, err)
		}
	}
}

// relaySourceSurface finishes the source's turns as the test sets them and
// reads their completions under its own lock, while the target's
// observation is gated.
type relaySourceSurface struct {
	*gatedSurface
	source string
	turnMu sync.Mutex
	turn   string
}

func (s *relaySourceSurface) finish(turn string) {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	s.turn = turn
}

func (s *relaySourceSurface) finished() *surface.TurnObservation {
	s.turnMu.Lock()
	defer s.turnMu.Unlock()
	return &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: s.turn, Reply: &surface.ReplyResult{Text: "reply " + s.turn, Done: true}}
}

func (s *relaySourceSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	if session.ID == s.source {
		return s.finished(), nil
	}
	return s.gatedSurface.Observe(ctx, session)
}

func (s *relaySourceSurface) ReadCompletions(_ context.Context, session *surface.Session, cursor string) (surface.CompletionBatch, error) {
	observation := s.finished()
	encoded, _ := json.Marshal(observation.CompletedTurnID)
	batch := surface.CompletionBatch{Cursor: string(encoded)}
	if cursor != "" && cursor != string(encoded) && session.ID == s.source {
		batch.Events = []surface.Completion{{CompletedAt: time.Now(), ID: observation.CompletedTurnID, Text: observation.Reply.Text}}
	}
	return batch, nil
}

func TestRelaysQueuedWhileTheirTargetIsObservedReachItInOrder(t *testing.T) {
	d, r, fake, from, to := daemonFixture(t)
	source := &relaySourceSurface{gatedSurface: newGatedSurface(fake), source: from.ID, turn: "turn-0"}
	d.Surfaces = []surface.Surface{source}
	if _, err := r.AddRoute(from.ID, to.ID, ".*"); err != nil {
		t.Fatal(err)
	}
	<-d.planScan(t.Context())
	source.gated[to.ID] = true
	queuedFor := func(count int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			counts, err := r.QueueCounts()
			if err != nil {
				t.Fatal(err)
			}
			if counts[to.ID] >= count {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("%d messages queued for the target, want %d", counts[to.ID], count)
			}
			time.Sleep(time.Millisecond)
		}
	}
	observed := source.callsOf(to.ID)
	source.finish("turn-1")
	targetScan := d.planScan(t.Context())
	waitUntil(t, "the target is being observed", func() bool { return source.callsOf(to.ID) > observed })
	queuedFor(1)
	waitUntil(t, "the source's job has finished", func() bool { return !d.observeJobs.holds(from.ID) })
	source.finish("turn-2")
	d.planScan(t.Context())
	queuedFor(2)
	if calls := source.callsOf(to.ID); calls != observed+1 {
		t.Fatalf("the target was observed %d more times while its observation ran, want once", calls-observed)
	}
	close(source.release)
	<-targetScan
	for range 5 {
		<-d.planScan(t.Context())
	}
	var replies []string
	for _, message := range fake.sentMessages() {
		for _, turn := range []string{"turn-1", "turn-2"} {
			if strings.Contains(message, "reply "+turn) {
				replies = append(replies, turn)
			}
		}
	}
	if !slices.Equal(replies, []string{"turn-1", "turn-2"}) {
		t.Fatalf("the target received the source's replies as %v, want turn-1 then turn-2 once each", replies)
	}
}

// sharingSurface reads one listing per scan through the scan its
// observations run in, the way the Claude surface lists its processes.
type sharingSurface struct {
	*daemonSurface
	listings atomic.Int32
}

type sharingListingKey struct{}

func (s *sharingSurface) Observe(ctx context.Context, session *surface.Session) (*surface.TurnObservation, error) {
	surface.ScanShared(ctx, sharingListingKey{}, time.Minute, func() bool {
		s.listings.Add(1)
		return true
	})
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}

func TestTheSessionsOfOneScanShareTheirSurfacesListing(t *testing.T) {
	d, r, fake, _, _ := daemonFixture(t)
	sharing := &sharingSurface{daemonSurface: fake}
	d.Surfaces = []surface.Surface{sharing}
	watchSessions(t, r, fake, surface.KindCodex, "one", "two", "three")
	<-d.planScan(t.Context())
	if listings := sharing.listings.Load(); listings != 1 {
		t.Fatalf("one scan of three sessions listed %d times, want once", listings)
	}
	<-d.planScan(t.Context())
	if listings := sharing.listings.Load(); listings != 2 {
		t.Fatalf("the next scan did not list again: %d listings in all", listings)
	}
}

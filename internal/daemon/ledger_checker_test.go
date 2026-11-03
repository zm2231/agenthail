package daemon

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

type checkerSurface struct {
	*daemonSurface
	mu       sync.Mutex
	started  []surface.CheckerStart
	stopped  []string
	startErr error
	onStart  func(surface.CheckerStart)
	stopGate chan struct{}
}

func (s *checkerSurface) StartChecker(_ context.Context, start surface.CheckerStart) (*surface.Session, error) {
	s.mu.Lock()
	if s.startErr != nil {
		s.mu.Unlock()
		return nil, s.startErr
	}
	s.started = append(s.started, start)
	onStart := s.onStart
	s.mu.Unlock()
	if onStart != nil {
		onStart(start)
	}
	return &surface.Session{ID: start.Run, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Cwd: start.Cwd, Status: surface.StatusBusy}, nil
}

func (s *checkerSurface) SessionAction(_ context.Context, session *surface.Session, action string) (map[string]any, error) {
	if action == "stop" && s.stopGate != nil {
		<-s.stopGate
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if action == "stop" {
		s.stopped = append(s.stopped, session.ID)
	}
	return map[string]any{"sessionId": session.ID}, nil
}

func (s *checkerSurface) snapshot() ([]surface.CheckerStart, []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]surface.CheckerStart(nil), s.started...), append([]string(nil), s.stopped...)
}

type checkerFixture struct {
	d       *Daemon
	r       *registry.Registry
	fake    *checkerSurface
	project ledgerProject
	worker  surface.Session
}

func newCheckerFixture(t *testing.T) checkerFixture {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	fake := &checkerSurface{daemonSurface: &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}}
	d := newTestDaemon(t, r, []surface.Surface{fake})
	p := addLedgerProject(t, r, "proj-check", "repo:github.com/acme/widget")
	if err := p.store.Enable(p.key); err != nil {
		t.Fatal(err)
	}
	return checkerFixture{d: d, r: r, fake: fake, project: p, worker: p.session(t, r, "worker")}
}

func (f checkerFixture) goal(t *testing.T, ref, name, words, clause string) string {
	t.Helper()
	u := ledger.Utterance{ProjectKey: f.project.key, Ref: ref, SpeakerID: surface.PersonSessionID, TargetID: f.worker.ID, Text: words, At: time.Now(), CapturedBy: ledger.CapturedByHook}
	if _, _, err := f.project.store.RecordUtterance(u); err != nil {
		t.Fatal(err)
	}
	g, _, err := f.project.store.EnsureGoal(f.project.key, name, f.worker.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.project.store.Attach(ledger.AttachRequest{ProjectKey: f.project.key, SessionID: f.worker.ID, UtteranceID: ref, As: ledger.AsNewAsk, To: g.ID, Clauses: []string{clause}}); err != nil {
		t.Fatal(err)
	}
	return g.ID
}

func (f checkerFixture) report(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	f.d.apiLedgerCheckReportHandler(rec, httptest.NewRequest(http.MethodPost, "/api/v1/ledger/check-report", strings.NewReader(body)))
	return rec
}

func reportBody(start surface.CheckerStart, token, rest string) string {
	return `{"run":"` + start.Run + `","token":"` + token + `",` + rest + `}`
}

func TestOneCheckerRunsAtATimeAndOnlyItsTokenReports(t *testing.T) {
	f := newCheckerFixture(t)
	first := f.goal(t, "claude-prompt:1", "Search", "build search with typo tolerance and a csv export", "build search")
	second := f.goal(t, "claude-prompt:2", "Billing", "add invoices and refunds", "add invoices")
	later := time.Now().Add(6 * time.Minute)
	f.d.sweepLedgerChecks(t.Context(), time.Now())
	if started, _ := f.fake.snapshot(); len(started) != 0 {
		t.Fatalf("a goal changed just now was checked: %+v", started)
	}
	f.d.sweepLedgerChecks(t.Context(), later)
	started, _ := f.fake.snapshot()
	if len(started) != 1 {
		t.Fatalf("checkers started = %d, want one per host", len(started))
	}
	run := started[0]
	if len(run.Token) != 64 || strings.Contains(run.Message, run.Token) {
		t.Fatalf("run token %q is not a secret kept out of the prompt", run.Token)
	}
	if !strings.Contains(run.Message, "build search with typo tolerance and a csv export") || !strings.Contains(run.Message, "widget-C1: build search") {
		t.Fatalf("checker input = %q", run.Message)
	}
	if run.Cwd != checkerDir(f.r) || strings.HasPrefix(run.Cwd, f.project.dir) {
		t.Fatalf("checker cwd = %q", run.Cwd)
	}
	if origin, found, err := f.r.SessionOrigin(run.Run); err != nil || !found || origin.Via != registry.OriginDaemon {
		t.Fatalf("checker origin = %+v %v %v", origin, found, err)
	}
	checker, err := f.r.Session(run.Run)
	if err != nil {
		t.Fatal(err)
	}
	if f.d.ledgerManaged(*checker) {
		t.Fatal("G2 would gate the checker")
	}
	if _, err := f.d.dispatcher().Deliver(t.Context(), f.fake, checker, "do something else", ""); !errors.Is(err, registry.ErrDaemonSession) {
		t.Fatalf("delivery to the checker = %v", err)
	}
	for _, action := range []string{"session-unarchive", "session-lifecycle-resume", "session-fork"} {
		_, err := f.d.doSessionAction(t.Context(), principal{Kind: principalPerson}, dashboardActionRequest{SessionID: run.Run, Action: action})
		var refused *actionError
		if !errors.As(err, &refused) || refused.Status != http.StatusConflict || refused.Code != "daemon_session" {
			t.Fatalf("%s on the checker = %v", action, err)
		}
	}
	f.d.sweepLedgerChecks(t.Context(), later.Add(time.Minute))
	if started, _ := f.fake.snapshot(); len(started) != 1 {
		t.Fatal("a second checker started while one runs")
	}

	for _, body := range []string{
		reportBody(run, "", `"none":true`),
		reportBody(run, strings.Repeat("0", 64), `"none":true`),
		`{"run":"not-running","token":"` + run.Token + `","none":true}`,
	} {
		if rec := f.report(body); rec.Code != http.StatusConflict {
			t.Fatalf("report %s = %d %s", body, rec.Code, rec.Body)
		}
	}
	stopping := make(chan struct{})
	f.fake.stopGate = stopping
	rec := f.report(reportBody(run, run.Token, `"missing":[{"breadth":"typo tolerance","from":"claude-prompt:1"},{"breadth":"csv export","from":"claude-prompt:1"}]`))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"cuts":["widget-X1","widget-X2"]`) {
		t.Fatalf("report = %d %s", rec.Code, rec.Body)
	}
	sweeping := make(chan struct{})
	go func() {
		f.d.sweepLedgerChecks(context.Background(), later.Add(2*time.Minute))
		close(sweeping)
	}()
	time.Sleep(100 * time.Millisecond)
	if started, _ := f.fake.snapshot(); len(started) != 1 {
		t.Fatal("the next checker started before the reported one was stopped")
	}
	close(stopping)
	<-sweeping
	if again := f.report(reportBody(run, run.Token, `"none":true`)); again.Code != http.StatusConflict {
		t.Fatalf("second report = %d %s", again.Code, again.Body)
	}
	testkit.Eventually(t, "the checker to be stopped and the next goal checked", 5*time.Second, func() bool {
		started, stopped := f.fake.snapshot()
		return len(stopped) == 1 && stopped[0] == run.Run && len(started) == 2
	})
	started, _ = f.fake.snapshot()
	if !strings.Contains(started[1].Message, second) || strings.Contains(started[1].Message, first) || started[1].Token == run.Token {
		t.Fatalf("second checker = %+v", started[1])
	}
	archived, err := f.r.ArchiveState()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived.Sessions[run.Run]; !ok {
		t.Fatalf("the finished checker %s is not archived", run.Run)
	}
	board, err := f.d.ledgerStore().Board(f.project.key)
	if err != nil {
		t.Fatal(err)
	}
	if checks := board.GoalChecks(); len(checks) != 2 || checks[0].CutsRaised != 2 {
		t.Fatalf("goal checks = %+v", checks)
	}
}

func TestACheckerThatReportsWhileItIsStillStartingIsHeard(t *testing.T) {
	f := newCheckerFixture(t)
	f.goal(t, "claude-prompt:1", "Search", "build search", "build search")
	reported := make(chan int, 1)
	f.fake.onStart = func(start surface.CheckerStart) {
		go func() { reported <- f.report(reportBody(start, start.Token, `"none":true`)).Code }()
		time.Sleep(50 * time.Millisecond)
	}
	f.d.sweepLedgerChecks(t.Context(), time.Now().Add(6*time.Minute))
	if code := <-reported; code != http.StatusOK {
		t.Fatalf("a report sent while the checker started = %d", code)
	}
	testkit.Eventually(t, "the reported checker to be stopped", 5*time.Second, func() bool {
		_, stopped := f.fake.snapshot()
		return len(stopped) == 1
	})
}

func TestACheckerThatNeverReportsTimesOutAndOnlyItIsStopped(t *testing.T) {
	f := newCheckerFixture(t)
	f.goal(t, "claude-prompt:1", "Search", "build search", "build search")
	start := time.Now().Add(6 * time.Minute)
	f.d.sweepLedgerChecks(t.Context(), start)
	started, _ := f.fake.snapshot()
	if len(started) != 1 {
		t.Fatalf("started = %d", len(started))
	}
	f.d.sweepLedgerChecks(t.Context(), start.Add(checkTimeout))
	_, stopped := f.fake.snapshot()
	if len(stopped) != 1 || stopped[0] != started[0].Run {
		t.Fatalf("stopped = %v, want only %s", stopped, started[0].Run)
	}
	board, err := f.d.ledgerStore().Board(f.project.key)
	if err != nil {
		t.Fatal(err)
	}
	if run, ok := board.CheckRun(started[0].Run); !ok || run.Result != ledger.CheckTimedOut {
		t.Fatalf("run = %+v", run)
	}
	if rec := f.report(reportBody(started[0], started[0].Token, `"none":true`)); rec.Code != http.StatusConflict {
		t.Fatalf("a report after the timeout = %d", rec.Code)
	}
}

func TestACheckerWhoseWordsAreReclassifiedIsStoppedAndTheNextCheckStarts(t *testing.T) {
	f := newCheckerFixture(t)
	f.goal(t, "claude-prompt:1", "Search", "build search", "build search")
	start := time.Now().Add(6 * time.Minute)
	f.d.sweepLedgerChecks(t.Context(), start)
	started, _ := f.fake.snapshot()
	if len(started) != 1 {
		t.Fatalf("started = %d", len(started))
	}
	billing := f.goal(t, "claude-prompt:2", "Billing", "add invoices", "add invoices")
	f.d.checks.mu.Lock()
	f.d.checks.enqueue(checkTarget{project: f.project.key, goal: billing}, false)
	f.d.checks.mu.Unlock()

	f.d.reclassifyHookCapture(f.worker, "claude-prompt:1", "scheduled")

	started, stopped := f.fake.snapshot()
	if len(stopped) != 1 || stopped[0] != started[0].Run {
		t.Fatalf("stopped = %v, want the voided checker %s at once", stopped, started[0].Run)
	}
	if len(started) != 2 || !strings.Contains(started[1].Message, billing) {
		t.Fatalf("the queued check did not start: %+v", started)
	}
	archived, err := f.r.ArchiveState()
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := archived.Sessions[started[0].Run]; !ok {
		t.Fatalf("the voided checker %s is not archived", started[0].Run)
	}
	if rec := f.report(reportBody(started[0], started[0].Token, `"missing":[{"breadth":"typo tolerance","from":"claude-prompt:1"}]`)); rec.Code != http.StatusConflict {
		t.Fatalf("the voided checker's report = %d %s", rec.Code, rec.Body)
	}
	f.d.checks.mu.Lock()
	running := f.d.checks.running
	f.d.checks.mu.Unlock()
	if running == nil || running.run != started[1].Run {
		t.Fatalf("running = %+v, want the billing check", running)
	}
}

func TestARestartFinishesAnInterruptedCheckByItsRecordedSession(t *testing.T) {
	f := newCheckerFixture(t)
	goal := f.goal(t, "claude-prompt:1", "Search", "build search", "build search")
	const recorded = "11111111-2222-4333-8444-555555555555"
	if _, err := f.project.store.StartCheck(f.project.key, goal, recorded, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RegisterSession(surface.Session{ID: recorded, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	f.d.sweepLedgerChecks(t.Context(), time.Now())
	_, stopped := f.fake.snapshot()
	if len(stopped) != 1 || stopped[0] != recorded {
		t.Fatalf("stopped = %v, want only the recorded checker", stopped)
	}
	board, err := f.d.ledgerStore().Board(f.project.key)
	if err != nil {
		t.Fatal(err)
	}
	if run, ok := board.CheckRun(recorded); !ok || run.Result != ledger.CheckInterrupted {
		t.Fatalf("run = %+v", run)
	}
}

func TestACheckerThatCannotStartIsRecordedAsFailed(t *testing.T) {
	f := newCheckerFixture(t)
	f.fake.startErr = errors.New("the Claude host opened it without the checker sandbox")
	goal := f.goal(t, "claude-prompt:1", "Search", "build search", "build search")
	f.d.sweepLedgerChecks(t.Context(), time.Now().Add(6*time.Minute))
	board, err := f.d.ledgerStore().Board(f.project.key)
	if err != nil {
		t.Fatal(err)
	}
	checks := board.GoalChecks()
	if len(checks) != 1 || checks[0].GoalID != goal || len(board.OpenChecks()) != 0 {
		t.Fatalf("checks = %+v open %+v", checks, board.OpenChecks())
	}
	if f.d.checks.running != nil {
		t.Fatal("a failed start still holds the checker slot")
	}
}

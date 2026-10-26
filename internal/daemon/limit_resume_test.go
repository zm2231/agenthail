package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type limitFixture struct {
	t       *testing.T
	d       *Daemon
	r       *registry.Registry
	fake    *daemonSurface
	dir     string
	enabled bool
}

func newLimitFixture(t *testing.T, ids ...string) *limitFixture {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	f := &limitFixture{t: t, r: r, dir: t.TempDir(), enabled: true}
	f.fake = &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	for _, id := range ids {
		session := surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: filepath.Join(f.dir, id+".jsonl")}
		if err := os.WriteFile(session.Transcript, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
		f.fake.sessions[id] = session
	}
	f.d = newTestDaemon(t, r, []surface.Surface{f.fake})
	f.d.limitResumeEnabled = func() bool { return f.enabled }
	return f
}

func (f *daemonSurface) UsageLimitTranscript(session *surface.Session) string {
	if session.Transcript != "" {
		return session.Transcript
	}
	return f.transcripts[session.ID]
}

func (f *daemonSurface) ReadUsageLimit(_ context.Context, session *surface.Session, _ string) (surface.UsageLimitEvidence, error) {
	f.limitReads.Add(1)
	observation := f.observations[session.ID]
	if observation == nil {
		return surface.UsageLimitEvidence{}, nil
	}
	return surface.UsageLimitEvidence{Account: observation.UsageAccount, SettledAt: observation.SettledAt, Stop: observation.LimitStop}, nil
}

func (f *limitFixture) observe(id string, at time.Time, observation *surface.TurnObservation) {
	f.t.Helper()
	f.fake.observations[id] = observation
	if err := os.Chtimes(filepath.Join(f.dir, id+".jsonl"), at, at); err != nil {
		f.t.Fatal(err)
	}
}

func (f *limitFixture) refused(id, account, turn string, refusedAt, resetsAt time.Time) {
	f.observe(id, refusedAt, &surface.TurnObservation{Status: surface.StatusIdle, UsageAccount: account, LimitStop: &surface.LimitStop{TurnID: turn, RefusedAt: refusedAt, ResetsAt: resetsAt}})
}

func (f *limitFixture) pass(now time.Time) {
	f.d.resumeAfterLimits(context.Background(), now)
}

func (f *limitFixture) state(id string) registry.LimitResumeState {
	f.t.Helper()
	resume, found, err := f.r.LimitResumeFor(id)
	if err != nil {
		f.t.Fatal(err)
	}
	if !found {
		return ""
	}
	return resume.State
}

func (f *limitFixture) queued(id string) []string {
	f.t.Helper()
	rows, err := f.r.ListQueue(false, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	var messages []string
	for _, row := range rows {
		if row.SessionID == id {
			messages = append(messages, row.Message)
		}
	}
	return messages
}

func TestAStoppedSessionResumesWithTheNoticeOnceItsLimitResets(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(2 * time.Hour)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)

	f.pass(refusedAt.Add(time.Minute))
	if state := f.state("worker"); state != registry.LimitResumeWaiting {
		t.Fatalf("state=%q, want waiting", state)
	}
	f.pass(resetsAt)
	if state := f.state("worker"); state != registry.LimitResumeWaiting {
		t.Fatalf("resumed before the grace after the reset: %q", state)
	}
	f.pass(resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeReleased {
		t.Fatalf("state=%q, want released", state)
	}
	if queued := f.queued("worker"); len(queued) != 1 || queued[0] != surface.LimitResumeNotice() {
		t.Fatalf("queued=%q", queued)
	}
	f.pass(resetsAt.Add(2 * limitResumeGrace))
	if queued := f.queued("worker"); len(queued) != 1 {
		t.Fatalf("the notice was queued twice: %q", queued)
	}
}

func TestAStoppedSessionWithQueuedMessagesResumesWithThem(t *testing.T) {
	f := newLimitFixture(t, "worker")
	if _, err := f.r.QueueMessageWithKey("worker", "next step", ""); err != nil {
		t.Fatal(err)
	}
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeReleased {
		t.Fatalf("state=%q", state)
	}
	if queued := f.queued("worker"); len(queued) != 1 || queued[0] != "next step" {
		t.Fatalf("queued=%q", queued)
	}
}

func TestSessionsOnOneAccountResumeOneAtATime(t *testing.T) {
	f := newLimitFixture(t, "older", "newer", "elsewhere")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.refused("older", "/roots/work", "u1", refusedAt, resetsAt)
	f.refused("newer", "/roots/work", "u1", refusedAt.Add(time.Second), resetsAt)
	f.refused("elsewhere", "/roots/home", "u1", refusedAt, resetsAt)

	due := resetsAt.Add(limitResumeGrace)
	f.pass(due)
	if f.state("newer") != registry.LimitResumeReleased || f.state("older") != registry.LimitResumeWaiting || f.state("elsewhere") != registry.LimitResumeReleased {
		t.Fatalf("newer=%q older=%q elsewhere=%q", f.state("newer"), f.state("older"), f.state("elsewhere"))
	}
	f.pass(due.Add(limitResumeSpacing - time.Second))
	if state := f.state("older"); state != registry.LimitResumeWaiting {
		t.Fatalf("the second session resumed within the spacing: %q", state)
	}
	f.pass(due.Add(limitResumeSpacing))
	if state := f.state("older"); state != registry.LimitResumeReleased {
		t.Fatalf("state=%q", state)
	}
}

func TestAnUnknownResetIsProbedByOneSessionAndItsSuccessReleasesTheRest(t *testing.T) {
	f := newLimitFixture(t, "third", "first", "second")
	refusedAt := time.Now().Add(-3 * time.Hour).Truncate(time.Second)
	f.refused("third", "/roots/work", "u1", refusedAt.Add(-time.Second), time.Time{})
	f.refused("first", "/roots/work", "u1", refusedAt, time.Time{})
	f.refused("second", "/roots/work", "u1", refusedAt.Add(time.Second), time.Time{})

	f.pass(refusedAt.Add(limitProbeInterval - time.Second))
	if f.state("first") != registry.LimitResumeWaiting || f.state("second") != registry.LimitResumeWaiting {
		t.Fatal("probed before the interval passed")
	}
	probeAt := refusedAt.Add(time.Second + limitProbeInterval)
	f.pass(probeAt)
	if f.state("second") != registry.LimitResumeReleased || f.state("first") != registry.LimitResumeWaiting {
		t.Fatalf("first=%q second=%q", f.state("first"), f.state("second"))
	}
	f.pass(probeAt.Add(2 * limitResumeSpacing))
	if state := f.state("first"); state != registry.LimitResumeWaiting {
		t.Fatalf("a second session was sent while the probe was out: %q", state)
	}

	settled := probeAt.Add(time.Minute)
	f.observe("second", settled, &surface.TurnObservation{Status: surface.StatusIdle, UsageAccount: "/roots/work", SettledAt: settled})
	f.pass(settled.Add(time.Second))
	if state := f.state("second"); state != "" {
		t.Fatalf("a stop stayed after its session settled: %q", state)
	}
	if state := f.state("first"); state != registry.LimitResumeReleased {
		t.Fatalf("the probe's success did not release the rest: %q", state)
	}
	f.pass(settled.Add(time.Second + limitResumeSpacing))
	if state := f.state("third"); state != registry.LimitResumeReleased {
		t.Fatalf("the probe's success did not release the rest: %q", state)
	}
}

func TestASuccessOnTheAccountReleasesBeforeTheStoredReset(t *testing.T) {
	f := newLimitFixture(t, "stopped", "other")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	f.refused("stopped", "/roots/proxy", "u1", refusedAt, refusedAt.Add(5*time.Hour))
	f.pass(refusedAt.Add(time.Second))
	settled := refusedAt.Add(10 * time.Minute)
	f.observe("other", settled, &surface.TurnObservation{Status: surface.StatusIdle, UsageAccount: "/roots/proxy", SettledAt: settled})
	f.pass(settled.Add(time.Second))
	if state := f.state("stopped"); state != registry.LimitResumeReleased {
		t.Fatalf("state=%q, want released", state)
	}
}

func TestAnUnknownResetLapsesAfterItsTries(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	at := refusedAt
	for try := 0; try < limitProbeTries; try++ {
		f.refused("worker", "/roots/work", "u"+string(rune('a'+try)), at, time.Time{})
		at = at.Add(limitProbeInterval)
		f.pass(at)
		if state := f.state("worker"); state != registry.LimitResumeReleased {
			t.Fatalf("try %d: state=%q", try, state)
		}
		at = at.Add(time.Minute)
	}
	f.refused("worker", "/roots/work", "last", at, time.Time{})
	f.pass(at.Add(limitProbeInterval))
	if state := f.state("worker"); state != registry.LimitResumeLapsed {
		t.Fatalf("state=%q, want lapsed", state)
	}
}

func TestAKnownResetLapsesADayAfterItPassed(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.enabled = false
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(refusedAt.Add(time.Second))
	f.enabled = true
	f.pass(resetsAt.Add(limitResumeLapse + time.Second))
	if state := f.state("worker"); state != registry.LimitResumeLapsed {
		t.Fatalf("state=%q, want lapsed", state)
	}
	if queued := f.queued("worker"); len(queued) != 0 {
		t.Fatalf("queued=%q", queued)
	}
}

func TestWithTheSettingOffAResetReleasesTheQueueWithoutANotice(t *testing.T) {
	f := newLimitFixture(t, "worker")
	f.enabled = false
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(refusedAt.Add(time.Second))
	if state := f.state("worker"); state != registry.LimitResumeWaiting {
		t.Fatalf("state=%q", state)
	}
	f.pass(resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeReleased {
		t.Fatalf("state=%q", state)
	}
	if queued := f.queued("worker"); len(queued) != 0 {
		t.Fatalf("queued=%q", queued)
	}
}

func TestACanceledResumeIsNotSent(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(refusedAt.Add(time.Second))
	if moved, err := f.r.TransitionLimitResume("worker", "u1", limitWaiting, registry.LimitResumeCanceled, time.Now()); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	f.pass(resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeCanceled {
		t.Fatalf("state=%q", state)
	}
	if queued := f.queued("worker"); len(queued) != 0 {
		t.Fatalf("queued=%q", queued)
	}
}

func TestAnUnchangedTranscriptIsNotReadAgain(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	f.refused("worker", "/roots/work", "u1", refusedAt, refusedAt.Add(time.Hour))
	f.pass(refusedAt.Add(time.Second))
	f.pass(refusedAt.Add(2 * time.Second))
	if reads := f.fake.limitReads.Load(); reads != 1 {
		t.Fatalf("read %d times", reads)
	}
}

func TestASessionWhoseRowNamesNoTranscriptIsReadFromTheOneItHas(t *testing.T) {
	f := newLimitFixture(t, "worker")
	session := f.fake.sessions["worker"]
	f.fake.transcripts = map[string]string{"worker": session.Transcript}
	session.Transcript = ""
	if err := f.r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	f.refused("worker", "/roots/work", "u1", refusedAt, refusedAt.Add(2*time.Hour))
	f.pass(refusedAt.Add(time.Minute))
	if state := f.state("worker"); state != registry.LimitResumeWaiting {
		t.Fatalf("state=%q, want waiting", state)
	}
}

func TestASessionWithoutATranscriptIsNotReadEachPass(t *testing.T) {
	f := newLimitFixture(t)
	session := surface.Session{ID: "exited", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: 1 << 30, Status: surface.StatusIdle}
	if err := f.r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	f.fake.sessions["exited"] = session
	now := time.Now()
	for pass := range 3 {
		f.pass(now.Add(time.Duration(pass) * limitResumeEvery))
	}
	if reads, observed := f.fake.limitReads.Load(), f.fake.observeCalls.Load(); reads != 0 || observed != 0 {
		t.Fatalf("read %d and observed %d times", reads, observed)
	}
}

func TestTheSessionShowsItsLimitStopAndItsActionsChangeIt(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newLimitFixture(t, "worker")
	projection := `{"id":"worker","surface":"claude","status":"idle"}`
	if _, _, err := f.r.RecordCatalogSession(registry.CatalogSessionState{Session: f.fake.sessions["worker"], Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now().UTC(), ProjectionFingerprint: projection}, registry.CatalogEvent{DedupeKey: "session.upserted:worker", Type: "session.upserted", EntityID: "worker", Payload: []byte(`{"session":` + projection + `}`)}); err != nil {
		t.Fatal(err)
	}
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Hour)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(refusedAt.Add(time.Second))
	handler := dashboardRouter(f.d)
	row := func() *dashboardLimitResume {
		t.Helper()
		f.d.publishCatalogPresence()
		for _, listed := range readDashboardState(t, handler).Sessions {
			if listed.ID == "worker" {
				return listed.LimitResume
			}
		}
		t.Fatal("session missing from the dashboard state")
		return nil
	}
	if stop := row(); stop == nil || stop.State != registry.LimitResumeWaiting || !stop.ResetsAt.Equal(resetsAt) || !stop.RefusedAt.Equal(refusedAt) {
		t.Fatalf("limitResume=%+v", stop)
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume-cancel","sessionId":"worker"}`); response.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	if stop := row(); stop == nil || stop.State != registry.LimitResumeCanceled {
		t.Fatalf("limitResume=%+v", stop)
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume","sessionId":"worker"}`); response.Code != http.StatusOK {
		t.Fatalf("resume status=%d body=%s", response.Code, response.Body.String())
	}
	if stop := row(); stop != nil {
		t.Fatalf("a resumed session still shows its stop: %+v", stop)
	}
	if queued := f.queued("worker"); len(queued) != 1 || queued[0] != surface.LimitResumeNotice() {
		t.Fatalf("queued=%q", queued)
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume","sessionId":"worker"}`); response.Code != http.StatusConflict {
		t.Fatalf("resuming a running session: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTheResumeAfterLimitSettingIsSaved(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	f := newLimitFixture(t)
	f.d.limitResumeEnabled = nil
	handler := dashboardRouter(f.d)
	if !f.d.resumeAfterLimitEnabled() {
		t.Fatal("resuming after a limit is off by default")
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", `{"action":"resume-after-limit","resumeAfterLimit":false}`); response.Code != http.StatusOK {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
	if f.d.resumeAfterLimitEnabled() {
		t.Fatal("turning the setting off did not stick")
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/settings", `{"action":"resume-after-limit","resumeAfterLimit":true}`); response.Code != http.StatusOK || !f.d.resumeAfterLimitEnabled() {
		t.Fatalf("settings status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestACancelDuringAPassIsNotOverriddenByTheStaleRow(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Hour).Truncate(time.Second)
	resetsAt := refusedAt.Add(time.Minute)
	f.refused("worker", "/roots/work", "u1", refusedAt, resetsAt)
	f.pass(refusedAt.Add(time.Second))
	read, err := f.r.LimitResumes()
	if err != nil || len(read) != 1 || read[0].State != registry.LimitResumeWaiting {
		t.Fatalf("read=%+v err=%v", read, err)
	}
	handler := dashboardRouter(f.d)
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume-cancel","sessionId":"worker"}`); response.Code != http.StatusOK {
		t.Fatalf("cancel status=%d body=%s", response.Code, response.Body.String())
	}
	f.d.resumeAccount("/roots/work", read, true, resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeCanceled {
		t.Fatalf("the pass released a canceled stop: %q", state)
	}
	if queued := f.queued("worker"); len(queued) != 0 {
		t.Fatalf("queued=%q", queued)
	}
	read[0].ResetsAt = time.Time{}
	read[0].Attempts = limitProbeTries
	f.d.resumeAccount("/roots/work", read, true, resetsAt.Add(limitResumeGrace))
	if state := f.state("worker"); state != registry.LimitResumeCanceled {
		t.Fatalf("the pass lapsed a canceled stop: %q", state)
	}
	if response := serveDashboardRequest(handler, http.MethodPost, "/api/v1/actions", `{"action":"limit-resume-cancel","sessionId":"worker"}`); response.Code != http.StatusConflict {
		t.Fatalf("canceling a canceled stop: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestTheCatalogReadsUsageLimitStopsOncePerPassWhateverItsSize(t *testing.T) {
	reads := func(count int) int {
		t.Helper()
		ids := make([]string, count)
		for index := range ids {
			ids[index] = fmt.Sprintf("worker-%02d", index)
		}
		f := newLimitFixture(t, ids...)
		refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
		for _, id := range ids {
			if err := f.r.RecordLimitStop(f.fake.sessions[id], "/roots/work", surface.LimitStop{TurnID: "u1", RefusedAt: refusedAt, ResetsAt: refusedAt.Add(time.Hour)}); err != nil {
				t.Fatal(err)
			}
		}
		var read atomic.Int32
		f.d.limitResumesRead = func() { read.Add(1) }
		f.d.discoverCatalog(context.Background())
		state, err := f.d.dashboardState(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		shown := 0
		for _, row := range state.Sessions {
			if row.LimitResume != nil && row.LimitResume.State == registry.LimitResumeWaiting {
				shown++
			}
		}
		if shown != count {
			t.Fatalf("%d of %d stopped sessions show their stop", shown, count)
		}
		return int(read.Load())
	}
	small, large := reads(2), reads(40)
	if small != large || small > 3 {
		t.Fatalf("stop reads: %d for 2 sessions, %d for 40", small, large)
	}
}

func TestAQueueConflictRereadsTheStopReleasedWithItsNotice(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := f.r.RecordLimitStop(f.fake.sessions["worker"], "/roots/work", surface.LimitStop{TurnID: "u1", RefusedAt: refusedAt, ResetsAt: refusedAt.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	stale, err := f.d.limitResumeRows()
	if err != nil || stale["worker"] == nil {
		t.Fatalf("stale=%v err=%v", stale, err)
	}
	if released, err := f.r.ReleaseLimitStop("worker", "u1", limitWaiting, surface.LimitResumeNotice(), "limit-resume:worker:u1", time.Now()); err != nil || !released {
		t.Fatalf("released=%v err=%v", released, err)
	}
	view, err := f.d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	if !f.d.publishCatalogSession(context.Background(), f.fake, f.fake.sessions["worker"], catalogIdentity{}, "", 0, stale["worker"], false, DashboardConfig{}, view, time.Now().UTC(), nil, nil) {
		t.Fatal("the session was not published")
	}
	window, err := f.r.CatalogEventsAfter(0, 50)
	if err != nil {
		t.Fatal(err)
	}
	var row dashboardSession
	for _, event := range window.Events {
		if event.Type != "session.upserted" || event.EntityID != "worker" {
			continue
		}
		var payload struct {
			Session dashboardSession `json:"session"`
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		row = payload.Session
	}
	if row.ID != "worker" || row.QueueCount != 1 || row.LimitResume != nil {
		t.Fatalf("published row: queue=%d limitResume=%+v", row.QueueCount, row.LimitResume)
	}
}

func TestTheNextPresencePassRepairsAStopPublishedBeforeACancel(t *testing.T) {
	f := newLimitFixture(t, "worker")
	refusedAt := time.Now().Add(-time.Minute).Truncate(time.Second)
	if err := f.r.RecordLimitStop(f.fake.sessions["worker"], "/roots/work", surface.LimitStop{TurnID: "u1", RefusedAt: refusedAt, ResetsAt: refusedAt.Add(time.Hour)}); err != nil {
		t.Fatal(err)
	}
	stale, err := f.d.limitResumeRows()
	if err != nil {
		t.Fatal(err)
	}
	if moved, err := f.r.TransitionLimitResume("worker", "u1", limitWaiting, registry.LimitResumeCanceled, time.Now()); err != nil || !moved {
		t.Fatalf("moved=%v err=%v", moved, err)
	}
	view, err := f.d.loadCatalogViewState()
	if err != nil {
		t.Fatal(err)
	}
	if !f.d.publishCatalogSession(context.Background(), f.fake, f.fake.sessions["worker"], catalogIdentity{}, "", 0, stale["worker"], false, DashboardConfig{}, view, time.Now().UTC(), nil, nil) {
		t.Fatal("the session was not published")
	}
	latest := func() *dashboardLimitResume {
		t.Helper()
		window, err := f.r.CatalogEventsAfter(0, 100)
		if err != nil {
			t.Fatal(err)
		}
		var stop *dashboardLimitResume
		for _, event := range window.Events {
			if event.EntityID != "worker" || event.Type != "session.upserted" {
				continue
			}
			var payload struct {
				Session dashboardSession `json:"session"`
			}
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			stop = payload.Session.LimitResume
		}
		return stop
	}
	if stop := latest(); stop == nil || stop.State != registry.LimitResumeWaiting {
		t.Fatalf("the stale publish did not land: %+v", stop)
	}
	f.d.publishCatalogPresence()
	if stop := latest(); stop == nil || stop.State != registry.LimitResumeCanceled {
		t.Fatalf("the presence pass left the stale stop: %+v", stop)
	}
}

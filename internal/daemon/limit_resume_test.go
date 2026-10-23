package daemon

import (
	"context"
	"os"
	"path/filepath"
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
	if _, err := f.r.CancelLimitResume("worker"); err != nil {
		t.Fatal(err)
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
	if calls := f.fake.observeCalls.Load(); calls != 1 {
		t.Fatalf("observed %d times", calls)
	}
}

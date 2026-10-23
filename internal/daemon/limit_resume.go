package daemon

import (
	"context"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	// limitSweepWindow is how far back a session's last activity may lie for
	// a usage-limit stop to be looked for: a weekly limit resets days later.
	limitSweepWindow = 8 * 24 * time.Hour
	// limitResumeGrace is waited after a reset before resuming, so the
	// provider has lifted the limit when the first resume reaches it.
	limitResumeGrace = 30 * time.Second
	// limitResumeSpacing separates the resumes of the sessions on one
	// account, so they do not all spend the new allowance at once.
	limitResumeSpacing = 30 * time.Second
	// limitResumeLapse is how long after its reset a stop is still resumed
	// automatically.
	limitResumeLapse = 24 * time.Hour
	// limitProbeInterval and limitProbeTries bound the resumes tried for a
	// limit whose reset time is unknown.
	limitProbeInterval = time.Hour
	limitProbeTries    = 6
	limitResumeEvery   = 30 * time.Second
)

// limitResumer remembers, between passes, what each session's transcript
// looked like when it was last read, when each account last had a turn end
// without an error, and when each account last had a session resumed.
type limitResumer struct {
	mu          sync.Mutex
	seen        map[string]time.Time
	settled     map[string]time.Time
	lastRelease map[string]time.Time
}

func newLimitResumer() *limitResumer {
	return &limitResumer{seen: map[string]time.Time{}, settled: map[string]time.Time{}, lastRelease: map[string]time.Time{}}
}

func (d *Daemon) runLimitResumes(ctx context.Context) {
	ticker := time.NewTicker(limitResumeEvery)
	defer ticker.Stop()
	d.resumeAfterLimits(ctx, time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			d.resumeAfterLimits(ctx, now)
		}
	}
}

// resumeAfterLimitEnabled reads the host's setting; it is on unless turned off.
func (d *Daemon) resumeAfterLimitEnabled() bool {
	if d.limitResumeEnabled != nil {
		return d.limitResumeEnabled()
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("read resume after limit setting: %s", err)
		return true
	}
	return config.ResumesAfterLimit()
}

// resumeAfterLimits records the usage-limit stops of this host's sessions and
// resumes those whose limit has reset. Only the host that runs a session
// resumes it; a paired host shows what this one publishes.
func (d *Daemon) resumeAfterLimits(ctx context.Context, now time.Time) {
	d.sweepLimitStops(ctx, now)
	resumes, err := d.Registry.LimitResumes()
	if err != nil {
		d.log.Printf("read usage-limit stops: %s", err)
		return
	}
	enabled := d.resumeAfterLimitEnabled()
	byAccount := map[string][]registry.LimitResume{}
	for _, resume := range resumes {
		byAccount[resume.AccountKey] = append(byAccount[resume.AccountKey], resume)
	}
	for account, stops := range byAccount {
		d.resumeAccount(account, stops, enabled, now)
	}
}

// sweepLimitStops reads each recently active local Claude session whose
// transcript changed since the last pass, recording a usage-limit stop and
// forgetting one that something ran after.
func (d *Daemon) sweepLimitStops(ctx context.Context, now time.Time) {
	sessions, err := d.Registry.ListSessions(0)
	if err != nil {
		d.log.Printf("list sessions for usage-limit stops: %s", err)
		return
	}
	recorded := map[string]registry.LimitResume{}
	if resumes, err := d.Registry.LimitResumes(); err == nil {
		for _, resume := range resumes {
			recorded[resume.SessionID] = resume
		}
	}
	for i := range sessions {
		session := &sessions[i]
		if session.Surface != surface.KindClaude {
			continue
		}
		changed, recent := d.limitResumes.transcriptChanged(session, now)
		if _, stopped := recorded[session.ID]; !recent && !stopped {
			continue
		}
		if !changed {
			continue
		}
		adapter, err := d.adapters().For(session)
		if err != nil {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		observation, err := adapter.Observe(operationCtx, session)
		cancel()
		if err != nil || observation == nil {
			d.limitResumes.forget(session.ID)
			continue
		}
		d.limitResumes.noteSettled(observation.UsageAccount, observation.SettledAt)
		if stop := observation.LimitStop; stop != nil {
			if err := d.Registry.RecordLimitStop(*session, observation.UsageAccount, *stop); err != nil {
				d.log.Printf("record usage-limit stop of %s: %s", session.ID, err)
			}
			continue
		}
		if resume, stopped := recorded[session.ID]; stopped {
			if err := d.Registry.ClearLimitStop(session.ID, resume.TurnID); err != nil {
				d.log.Printf("clear usage-limit stop of %s: %s", session.ID, err)
			}
		}
	}
}

// resumeAccount resumes the sessions waiting on one account. Every session
// on it waits for the same reset. A turn on the account that ends without an
// error after the earliest refusal shows the limit has lifted whatever reset
// was stored, since the account behind a config root can change between
// turns. With no reset time, one session tries a resume each interval and the
// rest follow only once it succeeds.
func (d *Daemon) resumeAccount(account string, stops []registry.LimitResume, enabled bool, now time.Time) {
	var waiting []registry.LimitResume
	var earliest, latest, resetsAt time.Time
	probing := false
	for _, stop := range stops {
		switch stop.State {
		case registry.LimitResumeReleased:
			probing = probing || stop.ResetsAt.IsZero()
			continue
		case registry.LimitResumeWaiting:
		default:
			continue
		}
		if !stop.ResetsAt.IsZero() && now.After(stop.ResetsAt.Add(limitResumeLapse)) || stop.ResetsAt.IsZero() && stop.Attempts >= limitProbeTries {
			d.setLimitResume(stop, registry.LimitResumeLapsed, now)
			continue
		}
		waiting = append(waiting, stop)
		if earliest.IsZero() || stop.RefusedAt.Before(earliest) {
			earliest = stop.RefusedAt
		}
		if stop.RefusedAt.After(latest) {
			latest = stop.RefusedAt
		}
		if stop.ResetsAt.After(resetsAt) {
			resetsAt = stop.ResetsAt
		}
	}
	if len(waiting) == 0 {
		return
	}
	sort.SliceStable(waiting, func(i, j int) bool { return waiting[i].RefusedAt.After(waiting[j].RefusedAt) })
	lifted := d.limitResumes.settledAfter(account, earliest)
	due := lifted || !resetsAt.IsZero() && !now.Before(resetsAt.Add(limitResumeGrace))
	if !enabled {
		if due || resetsAt.IsZero() {
			for _, stop := range waiting {
				d.setLimitResume(stop, registry.LimitResumeReleased, now)
			}
		}
		return
	}
	if !due {
		if !resetsAt.IsZero() || probing || now.Before(latest.Add(limitProbeInterval)) {
			return
		}
	}
	if last, ok := d.limitResumes.released(account); ok && now.Sub(last) < limitResumeSpacing {
		return
	}
	d.releaseLimitStop(waiting[0], true, now)
}

// releaseLimitStop lets a stopped session go. Its queued messages resume it
// when it has any; otherwise, when notice is set, Agenthail's resume notice
// does.
func (d *Daemon) releaseLimitStop(stop registry.LimitResume, notice bool, now time.Time) {
	if notice && d.Registry.QueueCount(stop.SessionID) == 0 {
		if _, err := d.Registry.QueueMessageWithKey(stop.SessionID, surface.LimitResumeNotice(), "limit-resume:"+stop.SessionID+":"+stop.TurnID); err != nil {
			d.log.Printf("queue resume of %s: %s", stop.SessionID, err)
			return
		}
	}
	d.setLimitResume(stop, registry.LimitResumeReleased, now)
	d.limitResumes.noteRelease(stop.AccountKey, now)
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "limit-resume", SessionID: stop.SessionID, Result: "resumed after usage limit"})
	d.requestObservation()
}

func (d *Daemon) setLimitResume(stop registry.LimitResume, state registry.LimitResumeState, now time.Time) {
	if err := d.Registry.SetLimitResumeState(stop.SessionID, stop.TurnID, state, now); err != nil {
		d.log.Printf("set usage-limit resume of %s: %s", stop.SessionID, err)
		return
	}
	d.publishEvent("state.changed", stop.SessionID, map[string]string{"source": "limit-resume"})
}

// transcriptChanged reports whether session's transcript changed since the
// last pass and whether the session was active within the sweep window. A
// session without a known transcript is always read.
func (l *limitResumer) transcriptChanged(session *surface.Session, now time.Time) (bool, bool) {
	stamp := session.LastActive
	if session.Transcript != "" {
		info, err := os.Stat(session.Transcript)
		if err != nil {
			return false, false
		}
		stamp = info.ModTime()
	}
	recent := stamp.IsZero() || now.Sub(stamp) < limitSweepWindow
	if session.Transcript == "" {
		return true, recent
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if previous, ok := l.seen[session.ID]; ok && previous.Equal(stamp) {
		return false, recent
	}
	l.seen[session.ID] = stamp
	return true, recent
}

func (l *limitResumer) forget(sessionID string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.seen, sessionID)
}

func (l *limitResumer) noteSettled(account string, at time.Time) {
	if account == "" || at.IsZero() {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if at.After(l.settled[account]) {
		l.settled[account] = at
	}
}

func (l *limitResumer) settledAfter(account string, at time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return account != "" && l.settled[account].After(at)
}

func (l *limitResumer) noteRelease(account string, at time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.lastRelease[account] = at
}

func (l *limitResumer) released(account string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at, ok := l.lastRelease[account]
	return at, ok
}

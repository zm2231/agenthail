package daemon

import (
	"context"
	"fmt"
	"net/http"
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
	moves       *limitMoves
}

func newLimitResumer() *limitResumer {
	return &limitResumer{seen: map[string]time.Time{}, settled: map[string]time.Time{}, lastRelease: map[string]time.Time{}, moves: newLimitMoves()}
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
	d.autoMoveLimitStops(ctx, now)
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

// sweepLimitStops reads the transcript of each local Claude session that
// changed recently and since the last pass, recording a usage-limit stop and
// forgetting one that something ran after. It reads transcripts only, so a
// pass asks nothing of the sessions' processes or of Claude.
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
		adapter, err := d.adapters().For(session)
		if err != nil {
			continue
		}
		reader, ok := adapter.(surface.UsageLimitReader)
		if !ok {
			continue
		}
		transcript := reader.UsageLimitTranscript(session)
		changed, recent := d.limitResumes.transcriptChanged(session.ID, transcript, now)
		if _, stopped := recorded[session.ID]; !recent && !stopped {
			continue
		}
		if !changed {
			continue
		}
		operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
		evidence, err := reader.ReadUsageLimit(operationCtx, session, transcript)
		cancel()
		if err != nil {
			d.limitResumes.forget(session.ID)
			continue
		}
		d.limitResumes.noteSettled(evidence.Account, evidence.SettledAt)
		if stop := evidence.Stop; stop != nil {
			if err := d.Registry.RecordLimitStop(*session, evidence.Account, *stop); err != nil {
				d.log.Printf("record usage-limit stop of %s: %s", d.resolveDisplay(session.ID), err)
			}
			continue
		}
		if resume, stopped := recorded[session.ID]; stopped {
			if err := d.Registry.ClearLimitStop(session.ID, resume.TurnID); err != nil {
				d.log.Printf("clear usage-limit stop of %s: %s", d.resolveDisplay(session.ID), err)
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
			d.logLimitResume(d.setLimitResume(stop, limitWaiting, registry.LimitResumeLapsed, now))
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
				d.logLimitResume(d.releaseLimitStop(stop, limitWaiting, false, now))
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
	d.logLimitResume(d.releaseLimitStop(waiting[0], limitWaiting, true, now))
}

var (
	limitWaiting = []registry.LimitResumeState{registry.LimitResumeWaiting}
	limitHeld    = []registry.LimitResumeState{registry.LimitResumeWaiting, registry.LimitResumeCanceled, registry.LimitResumeLapsed}
)

// releaseLimitStop lets a stopped session go if it is still in one of from.
// Its queued messages resume it when it has any; otherwise, when notice is
// set, Agenthail's resume notice does. It reports whether the stop was let go.
func (d *Daemon) releaseLimitStop(stop registry.LimitResume, from []registry.LimitResumeState, notice bool, now time.Time) (bool, error) {
	text, key := "", ""
	if notice {
		text, key = surface.LimitResumeNotice(), "limit-resume:"+stop.SessionID+":"+stop.TurnID
	}
	released, err := d.Registry.ReleaseLimitStop(stop.SessionID, stop.TurnID, from, text, key, now)
	if err != nil {
		return false, fmt.Errorf("resume %s after its usage limit: %w", d.resolveDisplay(stop.SessionID), err)
	}
	if !released {
		return false, nil
	}
	d.limitResumes.noteRelease(stop.AccountKey, now)
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "limit-resume", SessionID: stop.SessionID, Result: "resumed after usage limit"})
	d.limitResumeChanged(stop.SessionID)
	return true, nil
}

// setLimitResume moves a stop that is still in one of from to state.
func (d *Daemon) setLimitResume(stop registry.LimitResume, from []registry.LimitResumeState, state registry.LimitResumeState, now time.Time) (bool, error) {
	moved, err := d.Registry.TransitionLimitResume(stop.SessionID, stop.TurnID, from, state, now)
	if err != nil {
		return false, fmt.Errorf("set usage-limit resume of %s: %w", d.resolveDisplay(stop.SessionID), err)
	}
	if moved {
		d.limitResumeChanged(stop.SessionID)
	}
	return moved, nil
}

func (d *Daemon) limitResumeChanged(sessionID string) {
	d.limitResumes.moves.forget()
	d.publishEvent("state.changed", sessionID, map[string]string{"source": "limit-resume"})
	d.requestObservation()
}

func (d *Daemon) logLimitResume(_ bool, err error) {
	if err != nil {
		d.log.Printf("%s", err)
	}
}

// transcriptChanged reports whether a session's transcript changed since the
// last pass and whether it changed within the sweep window. A session with no
// transcript has nothing to read.
func (l *limitResumer) transcriptChanged(sessionID, transcript string, now time.Time) (bool, bool) {
	if transcript == "" {
		return false, false
	}
	info, err := os.Stat(transcript)
	if err != nil {
		return false, false
	}
	stamp := info.ModTime()
	recent := now.Sub(stamp) < limitSweepWindow
	l.mu.Lock()
	defer l.mu.Unlock()
	if previous, ok := l.seen[sessionID]; ok && previous.Equal(stamp) {
		return false, recent
	}
	l.seen[sessionID] = stamp
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

// dashboardLimitResume is a session's usage-limit stop as clients show it.
// A released stop is not shown: the session is running again.
type dashboardLimitResume struct {
	State     registry.LimitResumeState `json:"state"`
	RefusedAt time.Time                 `json:"refusedAt"`
	ResetsAt  time.Time                 `json:"resetsAt,omitzero"`
	MoveTo    *limitMoveOffer           `json:"moveTo,omitempty"`
}

func sameLimitResume(a, b *dashboardLimitResume) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.State == b.State && a.RefusedAt.Equal(b.RefusedAt) && a.ResetsAt.Equal(b.ResetsAt) && sameLimitMoveOffer(a.MoveTo, b.MoveTo)
}

func (d *Daemon) limitResumeRow(sessionID string) *dashboardLimitResume {
	if d.limitResumesRead != nil {
		d.limitResumesRead()
	}
	resume, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil || !found || resume.State == registry.LimitResumeReleased {
		return nil
	}
	return &dashboardLimitResume{State: resume.State, RefusedAt: resume.RefusedAt, ResetsAt: resume.ResetsAt, MoveTo: d.limitMoveOffer(sessionID, time.Now())}
}

// limitResumeRows reads every shown stop at once, so a catalog pass reads
// them once however many sessions it projects.
func (d *Daemon) limitResumeRows() (map[string]*dashboardLimitResume, error) {
	if d.limitResumesRead != nil {
		d.limitResumesRead()
	}
	resumes, err := d.Registry.LimitResumes()
	if err != nil {
		return nil, err
	}
	rows := map[string]*dashboardLimitResume{}
	offers := d.cachedLimitMoveOffers(time.Now())
	for _, resume := range resumes {
		if resume.State == registry.LimitResumeReleased {
			continue
		}
		rows[resume.SessionID] = &dashboardLimitResume{State: resume.State, RefusedAt: resume.RefusedAt, ResetsAt: resume.ResetsAt, MoveTo: offers[resume.SessionID]}
	}
	return rows, nil
}

// limitResumeAction resumes a stopped session now, whatever its reset, or
// keeps it from being resumed for its current stop.
func (d *Daemon) limitResumeAction(w http.ResponseWriter, sessionID string, resume bool) {
	stop, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !found || stop.State == registry.LimitResumeReleased {
		writeAPIError(w, http.StatusConflict, "conflict", "this session is not stopped by a usage limit")
		return
	}
	var moved bool
	if resume {
		moved, err = d.releaseLimitStop(stop, limitHeld, true, time.Now())
	} else {
		moved, err = d.setLimitResume(stop, limitWaiting, registry.LimitResumeCanceled, time.Now())
	}
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	if !moved {
		writeAPIError(w, http.StatusConflict, "conflict", "the session's usage-limit stop changed; check it again")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
}

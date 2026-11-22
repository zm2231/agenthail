package daemon

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/dashconfig"
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
// without an error, when each account last had a session resumed, and which
// stops a restart failed for.
type limitResumer struct {
	mu          sync.Mutex
	seen        map[string]time.Time
	settled     map[string]time.Time
	lastRelease map[string]time.Time
	restarts    map[string]string
	moves       *limitMoves
	claims      limitMoveClaims
}

func newLimitResumer() *limitResumer {
	return &limitResumer{seen: map[string]time.Time{}, settled: map[string]time.Time{}, lastRelease: map[string]time.Time{}, restarts: map[string]string{}, moves: newLimitMoves()}
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
	config, err := dashconfig.Load()
	if err != nil {
		d.log.Printf("read resume after limit setting: %s", err)
		return true
	}
	return config.ResumesAfterLimit()
}

// limitResumeSwitch answers, for one pass, whether a stopped session
// resumes: its project's limit-resume setting, or the host's when that is
// host or the project cannot be read.
func (d *Daemon) limitResumeSwitch() func(sessionID string) bool {
	hostSetting := d.resumeAfterLimitEnabled()
	answers := map[string]bool{}
	return func(sessionID string) bool {
		if answer, seen := answers[sessionID]; seen {
			return answer
		}
		answer := hostSetting
		if session, err := d.Registry.Session(sessionID); err == nil {
			if project, _, err := d.recordedProject(*session); err == nil && project != nil {
				switch value, _ := d.Registry.ProjectSettingValue(project.ID, registry.ProjectSettingLimitResume); value {
				case registry.ProjectLimitResumeOn:
					answer = true
				case registry.ProjectLimitResumeOff:
					answer = false
				}
			}
		}
		answers[sessionID] = answer
		return answer
	}
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
	resumeOn, moves := d.limitResumeSwitch(), d.moveAfterLimitEnabled()
	byAccount := map[string][]registry.LimitResume{}
	for _, resume := range resumes {
		byAccount[resume.AccountKey] = append(byAccount[resume.AccountKey], resume)
	}
	for account, stops := range byAccount {
		d.resumeAccount(account, stops, resumeOn, moves, now)
	}
}

// sweepLimitStops reads the transcript of each local Claude session that
// changed recently and since the last pass, recording a usage-limit stop and
// forgetting one that something ran after. It reads transcripts only, so a
// pass asks nothing of the sessions' processes or of Claude.
func (d *Daemon) sweepLimitStops(ctx context.Context, now time.Time) {
	sessions, err := d.Registry.ListSessionsOf(surface.KindClaude)
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
	transcripts := map[surface.UsageLimitReader]func(*surface.Session) string{}
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
		if transcripts[reader] == nil {
			transcripts[reader] = reader.UsageLimitTranscripts()
		}
		transcript := transcripts[reader](session)
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
	d.limitResumes.moves.forget()
}

// resumeAccount resumes the sessions stopped on one config root, each by
// its own evidence. A running Claude process keeps the limit it was told
// until the reset it named, so a stop is resumed by a message only once its
// own reset has passed. Before that, a turn on the root that ends without an
// error after the refusal shows the account behind the root has room, which
// only a new process sees: a hosted session is restarted once and resumed,
// and any other gets a note to restart it. A hosted session on a proxy root
// is restarted once when the proxy has room; one on a direct root without
// room moves once to a proxy root with room when moves are on (limitSwitch).
// A stop that follows a refused resume or move is tried once more, at its
// own reset if that is still ahead, and never on room elsewhere. With no
// reset time, one session tries a resume each interval, a bounded number of
// times.
func (d *Daemon) resumeAccount(account string, stops []registry.LimitResume, resumeOn func(string) bool, moves bool, now time.Time) {
	var due []limitRelease
	var probes []registry.LimitResume
	var view *proxyView
	var latest time.Time
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
		if limitStopLapsed(stop, now) {
			d.logLimitResume(d.setLimitResume(stop, limitWaiting, registry.LimitResumeLapsed, now))
			continue
		}
		if !resumeOn(stop.SessionID) {
			if stop.ResetsAt.IsZero() || !now.Before(stop.ResetsAt.Add(limitResumeGrace)) {
				d.logLimitResume(d.releaseLimitStop(stop, limitWaiting, false, now))
			}
			continue
		}
		if !stop.ResetsAt.IsZero() && !now.Before(stop.ResetsAt.Add(limitResumeGrace)) {
			due = append(due, limitRelease{stop: stop})
			continue
		}
		if stop.Attempts == 0 {
			if view == nil {
				read := d.readProxyView(now)
				view = &read
			}
			if release, ok := d.limitSwitch(*view, stop, moves, now); ok {
				due = append(due, release)
				continue
			}
		}
		if stop.ResetsAt.IsZero() {
			probes = append(probes, stop)
			if stop.RefusedAt.After(latest) {
				latest = stop.RefusedAt
			}
		}
	}
	if last, ok := d.limitResumes.released(account); ok && now.Sub(last) < limitResumeSpacing {
		return
	}
	sort.SliceStable(due, func(i, j int) bool { return due[i].stop.RefusedAt.After(due[j].stop.RefusedAt) })
	for _, release := range due {
		switch {
		case release.moveTo != "":
			if d.moveLimitedSession(release, now) {
				return
			}
		case release.restart:
			if d.restartLimitedSession(release.stop, now) {
				return
			}
		default:
			d.logLimitResume(d.releaseLimitStop(release.stop, limitWaiting, true, now))
			return
		}
	}
	if len(due) > 0 || len(probes) == 0 || probing || now.Before(latest.Add(limitProbeInterval)) {
		return
	}
	sort.SliceStable(probes, func(i, j int) bool { return probes[i].RefusedAt.After(probes[j].RefusedAt) })
	d.logLimitResume(d.releaseLimitStop(probes[0], limitWaiting, true, now))
}

type limitRelease struct {
	stop    registry.LimitResume
	restart bool
	moveTo  string
	moveVia string
}

// limitStopLapsed reports whether a stop is past being resumed
// automatically: a day after its reset, after its tries when it has no reset,
// or when a resume was refused after the reset it waited for.
func limitStopLapsed(stop registry.LimitResume, now time.Time) bool {
	if stop.ResetsAt.IsZero() {
		return stop.Attempts >= limitProbeTries
	}
	return now.After(stop.ResetsAt.Add(limitResumeLapse)) || stop.Attempts > 0 && !stop.RefusedAt.Before(stop.ResetsAt)
}

// restartLimitedSession restarts a hosted session whose root has room so its
// next turn runs in a new process, then resumes it. It reports whether it
// acted. A restart that fails counts as the stop's attempt, so the stop then
// waits for its own reset and is never restarted again.
func (d *Daemon) restartLimitedSession(stop registry.LimitResume, now time.Time) bool {
	session, err := d.Registry.Session(stop.SessionID)
	if err != nil || session.RuntimeKind != surface.RuntimeClaudeHost {
		return false
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		d.failLimitRestart(stop, err.Error(), now)
		return false
	}
	lifecycle, ok := adapter.(surface.SessionLifecycle)
	if !ok {
		d.failLimitRestart(stop, "its surface cannot stop it", now)
		return false
	}
	acted := false
	if !d.unlessLimitMoving(stop.SessionID, func() {
		ctx, cancel := context.WithTimeout(context.Background(), surfaceOperationTimeout)
		defer cancel()
		if _, err := lifecycle.SessionAction(ctx, session, "stop"); err != nil {
			d.failLimitRestart(stop, err.Error(), now)
			return
		}
		acted = true
		d.logLimitResume(d.releaseLimitStop(stop, limitWaiting, true, now))
	}) {
		return false
	}
	return acted
}

func (d *Daemon) failLimitRestart(stop registry.LimitResume, reason string, now time.Time) {
	d.log.Printf("restart %s after its usage limit: %s", d.resolveDisplay(stop.SessionID), reason)
	d.limitResumes.noteRestartFailure(stop, reason)
	if _, err := d.Registry.CountLimitAttempt(stop.SessionID, stop.TurnID, now); err != nil {
		d.log.Printf("count the restart of %s: %s", d.resolveDisplay(stop.SessionID), err)
	}
	d.limitResumeChanged(stop.SessionID)
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

func limitStopKey(stop registry.LimitResume) string {
	return stop.SessionID + "\x00" + stop.TurnID
}

func (l *limitResumer) noteRestartFailure(stop registry.LimitResume, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.restarts[limitStopKey(stop)] = reason
}

func (l *limitResumer) restartFailure(stop registry.LimitResume) (string, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	reason, ok := l.restarts[limitStopKey(stop)]
	return reason, ok
}

func (l *limitResumer) released(account string) (time.Time, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	at, ok := l.lastRelease[account]
	return at, ok
}

// dashboardLimitResume is a session's usage-limit stop as clients show it.
// A released stop is not shown: the session is running again. ResetsAt is
// when it resumes: its own reset, or sooner when a full proxy it would
// switch through frees an account first.
type dashboardLimitResume struct {
	State     registry.LimitResumeState `json:"state"`
	RefusedAt time.Time                 `json:"refusedAt"`
	ResetsAt  time.Time                 `json:"resetsAt,omitzero"`
	Note      string                    `json:"note,omitempty"`
	MoveTo    *limitMoveOffer           `json:"moveTo,omitempty"`
}

func sameLimitResume(a, b *dashboardLimitResume) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.State == b.State && a.RefusedAt.Equal(b.RefusedAt) && a.ResetsAt.Equal(b.ResetsAt) && a.Note == b.Note && sameLimitMoveOffer(a.MoveTo, b.MoveTo)
}

func (d *Daemon) limitResumeRow(sessionID string) *dashboardLimitResume {
	if d.limitResumesRead != nil {
		d.limitResumesRead()
	}
	resume, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil || !found || resume.State == registry.LimitResumeReleased {
		return nil
	}
	return d.newLimitResumeProjection(time.Now()).row(resume)
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
	projection := d.newLimitResumeProjection(time.Now())
	for _, resume := range resumes {
		if resume.State != registry.LimitResumeReleased {
			rows[resume.SessionID] = projection.row(resume)
		}
	}
	return rows, nil
}

// limitResumeProjection shows stops as clients see them, reading the settings,
// the roots and the proxies' cached accounts once, and only once a waiting
// stop needs them, for every stop it shows.
type limitResumeProjection struct {
	d        *Daemon
	now      time.Time
	read     bool
	resumeOn func(string) bool
	moves    bool
	view     proxyView
	offers   map[string]*limitMoveOffer
}

func (d *Daemon) newLimitResumeProjection(now time.Time) *limitResumeProjection {
	return &limitResumeProjection{d: d, now: now}
}

func (p *limitResumeProjection) row(stop registry.LimitResume) *dashboardLimitResume {
	row := &dashboardLimitResume{State: stop.State, RefusedAt: stop.RefusedAt, ResetsAt: stop.ResetsAt, Note: p.d.limitResumeNote(stop, p.now)}
	if stop.State != registry.LimitResumeWaiting {
		return row
	}
	if !p.read {
		p.read = true
		p.resumeOn, p.moves = p.d.limitResumeSwitch(), p.d.moveAfterLimitEnabled()
		p.view = p.d.readProxyView(p.now)
		p.offers = p.d.cachedLimitMoveOffers(p.view, p.now)
	}
	row.ResetsAt = p.d.limitResumesAt(p.view, stop, p.resumeOn(stop.SessionID), p.moves, p.now)
	row.MoveTo = p.offers[stop.SessionID]
	return row
}

func (d *Daemon) limitResumeNote(stop registry.LimitResume, now time.Time) string {
	if reason, failed := d.limitResumes.restartFailure(stop); failed && stop.State == registry.LimitResumeWaiting {
		return "Could not restart it to resume: " + reason + "; it tries once more at its reset"
	}
	refusedAgain := stop.Attempts > 0 && !stop.ResetsAt.IsZero()
	switch stop.State {
	case registry.LimitResumeLapsed:
		if refusedAgain && !stop.RefusedAt.Before(stop.ResetsAt) {
			return "Resuming was refused after its reset; resume it when the limit lifts"
		}
	case registry.LimitResumeWaiting:
		if refusedAgain {
			return "Resuming didn't go through; it tries once more at its reset"
		}
		if !d.limitResumes.settledAfter(stop.AccountKey, stop.RefusedAt) {
			return ""
		}
		if session, err := d.Registry.Session(stop.SessionID); err == nil && session.RuntimeKind != surface.RuntimeClaudeHost {
			return "Its account has room again; restart it to continue"
		}
	}
	return ""
}

// doLimitResume resumes a stopped session now, whatever its reset, or
// keeps it from being resumed for its current stop.
func (d *Daemon) doLimitResume(sessionID string, resume bool) (actionResult, error) {
	stop, found, err := d.Registry.LimitResumeFor(sessionID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if !found || stop.State == registry.LimitResumeReleased {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", "this session is not stopped by a usage limit")
	}
	var moved bool
	if !d.unlessLimitMoving(sessionID, func() {
		if resume {
			moved, err = d.releaseLimitStop(stop, limitHeld, true, time.Now())
		} else {
			moved, err = d.setLimitResume(stop, limitWaiting, registry.LimitResumeCanceled, time.Now())
		}
	}) {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", "this session is moving to another Claude account")
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if !moved {
		return actionResult{}, actionFailed(http.StatusConflict, "conflict", "the session's usage-limit stop changed; check it again")
	}
	return actionOK(map[string]any{"ok": true}), nil
}

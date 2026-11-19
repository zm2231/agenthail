package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Covers the judge's 15 s deadline and a queued peer message's follow-up turn, seen starting up to 13 s after the turn before it ended.
const turnPushSettle = 20 * time.Second

const (
	settledOnVerdict  = "verdict"
	settledOnIdle     = "idle"
	settledOnDeadline = "deadline"
)

const (
	verdictQueued = iota + 1
	verdictDone
	verdictNeedsYou
)

type turnVerdict struct {
	turnID string
	state  int
}

type pendingTurnPush struct {
	turnID string
	turns  []string
	notice turnCompletedNotice
	paused bool
	timer  *time.Timer
}

func (entry *pendingTurnPush) pause() {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.paused = true
}

type turnPushes struct {
	mu       sync.Mutex
	window   time.Duration
	pending  map[string]*pendingTurnPush
	verdicts map[string]turnVerdict
}

func newTurnPushes() *turnPushes {
	return &turnPushes{window: turnPushSettle, pending: map[string]*pendingTurnPush{}, verdicts: map[string]turnVerdict{}}
}

func (p *turnPushes) judging(sessionID, turnID string) bool {
	verdict := p.verdicts[sessionID]
	return verdict.turnID == turnID && verdict.state == verdictQueued
}

func (p *turnPushes) remove(sessionID string, entry *pendingTurnPush) {
	delete(p.pending, sessionID)
	if entry.timer != nil {
		entry.timer.Stop()
	}
}

func (d *Daemon) turnJudgmentQueued(sessionID, turnID string) {
	p := d.turnPushes
	p.mu.Lock()
	p.verdicts[sessionID] = turnVerdict{turnID: turnID, state: verdictQueued}
	p.mu.Unlock()
}

// A session pushes once per burst of turns: a turn's finished push waits for its verdict, or for the session to go idle, and a newer turn of the session takes its place.
func (d *Daemon) scheduleTurnPush(sessionID, turnID string, notice turnCompletedNotice) {
	p := d.turnPushes
	entry := &pendingTurnPush{turnID: turnID, notice: notice}
	p.mu.Lock()
	prior := p.pending[sessionID]
	if prior != nil {
		p.remove(sessionID, prior)
		entry.turns = append(entry.turns, prior.turns...)
	}
	entry.turns = append(entry.turns, turnID)
	if verdict := p.verdicts[sessionID]; verdict.turnID == turnID && verdict.state == verdictNeedsYou {
		p.mu.Unlock()
		return
	}
	p.pending[sessionID] = entry
	p.mu.Unlock()
	d.settleTurnPush(sessionID, nil, settledOnIdle)
}

// The deadline sends whatever the verdict, but never while the session runs another turn, which a delivery records before the observer sees it.
func (d *Daemon) settleTurnPush(sessionID string, expected *pendingTurnPush, reason string) {
	queued := d.Registry.QueueCount(sessionID) > 0
	runtime, found, err := d.Registry.RuntimeState(sessionID)
	if err != nil {
		d.log.Printf("read the runtime of %s to settle its push: %s", d.resolveDisplay(sessionID), err)
	}
	p := d.turnPushes
	p.mu.Lock()
	entry := p.pending[sessionID]
	if entry == nil || entry.paused || expected != nil && entry != expected {
		p.mu.Unlock()
		return
	}
	running := found && runtime.LastStatus == surface.StatusBusy && runtime.ActiveTurnID != entry.turnID
	switch {
	case running:
		entry.pause()
	case reason == settledOnDeadline || !queued && !p.judging(sessionID, entry.turnID):
		p.remove(sessionID, entry)
		p.mu.Unlock()
		d.sendSettledTurnPush(sessionID, entry, reason)
		return
	case entry.timer == nil:
		entry.timer = time.AfterFunc(p.window, func() {
			d.background.start(func(context.Context) { d.settleTurnPush(sessionID, entry, settledOnDeadline) })
		})
	}
	p.mu.Unlock()
}

func (d *Daemon) sendSettledTurnPush(sessionID string, entry *pendingTurnPush, reason string) {
	d.logSettledPush(sessionID, entry.turns, reason, "turn.completed")
	d.recordTurnNotice(sessionID, entry.turnID, entry.notice)
	d.notifyTurnCompleted(entry.notice, sessionID, entry.turnID)
}

func (d *Daemon) logSettledPush(sessionID string, turns []string, reason, event string) {
	d.log.Printf("push settled session=%s turns=%s reason=%s event=%s", sessionID, strings.Join(turns, ","), reason, event)
}

// A viewer mirrors the notice, so it is recorded only for the turn whose push went out.
func (d *Daemon) recordTurnNotice(sessionID, turnID string, notice turnCompletedNotice) {
	recorded := &registry.TurnNotice{TurnID: turnID, Outcome: notice.outcome, RequestID: notice.requestID, At: time.Now()}
	if err := d.Registry.RecordCompletion(sessionID, recorded, nil); err != nil {
		d.log.Printf("record completion %s: %s", d.resolveDisplay(sessionID), err)
		return
	}
	d.requestCatalogDiscovery()
}

func (d *Daemon) pauseTurnPush(sessionID string) {
	p := d.turnPushes
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.pending[sessionID]; entry != nil {
		entry.pause()
	}
}

func (d *Daemon) resumeTurnPush(sessionID string) {
	p := d.turnPushes
	p.mu.Lock()
	entry := p.pending[sessionID]
	if entry == nil || !entry.paused {
		p.mu.Unlock()
		return
	}
	entry.paused = false
	p.mu.Unlock()
	d.settleTurnPush(sessionID, entry, settledOnIdle)
}

func (d *Daemon) dropTurnPush(sessionID string) {
	p := d.turnPushes
	p.mu.Lock()
	defer p.mu.Unlock()
	if entry := p.pending[sessionID]; entry != nil {
		p.remove(sessionID, entry)
	}
}

func (d *Daemon) turnVerdictLanded(sessionID, turnID string) {
	p := d.turnPushes
	p.mu.Lock()
	if verdict := p.verdicts[sessionID]; verdict.turnID == turnID {
		p.verdicts[sessionID] = turnVerdict{turnID: turnID, state: verdictDone}
	}
	entry := p.pending[sessionID]
	p.mu.Unlock()
	if entry != nil && entry.turnID == turnID {
		d.settleTurnPush(sessionID, entry, settledOnVerdict)
	}
}

// The needs-you push stands for the turn's finished push, and for the burst it ends.
func (d *Daemon) needsYouTakesTurnPush(sessionID, turnID string) {
	p := d.turnPushes
	p.mu.Lock()
	p.verdicts[sessionID] = turnVerdict{turnID: turnID, state: verdictNeedsYou}
	turns := []string{turnID}
	if entry := p.pending[sessionID]; entry != nil && entry.turnID == turnID {
		p.remove(sessionID, entry)
		turns = entry.turns
	}
	p.mu.Unlock()
	d.logSettledPush(sessionID, turns, settledOnVerdict, turnNeedsYouEvent)
}

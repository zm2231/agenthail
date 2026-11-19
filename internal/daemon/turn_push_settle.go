package daemon

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// Covers the judge's 15 s deadline and a queued peer message's follow-up turn, seen starting up to 13 s after the turn before it ended.
const turnPushSettle = 20 * time.Second

const (
	settledOnVerdict  = "verdict"
	settledOnIdle     = "idle"
	settledOnDeadline = "deadline"
)

type pendingTurnPush struct {
	turnID  string
	turns   []string
	notice  turnCompletedNotice
	judging bool
	paused  bool
	timer   *time.Timer
}

type turnPushes struct {
	mu      sync.Mutex
	window  time.Duration
	pending map[string]*pendingTurnPush
}

func newTurnPushes() *turnPushes {
	return &turnPushes{window: turnPushSettle, pending: map[string]*pendingTurnPush{}}
}

func (p *turnPushes) take(sessionID string, entry *pendingTurnPush) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.pending[sessionID] != entry {
		return false
	}
	delete(p.pending, sessionID)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	return true
}

// A session pushes once per burst of turns: a turn's finished push waits for its verdict, or for the session to go idle, and a newer turn of the session takes its place.
func (d *Daemon) scheduleTurnPush(sessionID, turnID string, notice turnCompletedNotice, judging bool) {
	p := d.turnPushes
	entry := &pendingTurnPush{turnID: turnID, notice: notice, judging: judging}
	p.mu.Lock()
	if prior := p.pending[sessionID]; prior != nil {
		if prior.timer != nil {
			prior.timer.Stop()
		}
		entry.turns = append(entry.turns, prior.turns...)
	}
	entry.turns = append(entry.turns, turnID)
	p.pending[sessionID] = entry
	p.mu.Unlock()
	d.settleTurnPush(sessionID, settledOnIdle)
}

func (d *Daemon) settleTurnPush(sessionID, reason string) {
	queued := d.Registry.QueueCount(sessionID) > 0
	p := d.turnPushes
	p.mu.Lock()
	entry := p.pending[sessionID]
	if entry == nil || entry.paused {
		p.mu.Unlock()
		return
	}
	if !entry.judging && !queued {
		p.mu.Unlock()
		d.sendSettledTurnPush(sessionID, entry, reason)
		return
	}
	if entry.timer == nil {
		entry.timer = time.AfterFunc(p.window, func() {
			d.background.start(func(context.Context) { d.sendSettledTurnPush(sessionID, entry, settledOnDeadline) })
		})
	}
	p.mu.Unlock()
}

func (d *Daemon) sendSettledTurnPush(sessionID string, entry *pendingTurnPush, reason string) {
	if !d.turnPushes.take(sessionID, entry) {
		return
	}
	d.log.Printf("push settled session=%s turns=%s reason=%s event=turn.completed", sessionID, strings.Join(entry.turns, ","), reason)
	d.recordTurnNotice(sessionID, entry.turnID, entry.notice)
	d.notifyTurnCompleted(entry.notice, sessionID, entry.turnID)
}

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
		if entry.timer != nil {
			entry.timer.Stop()
			entry.timer = nil
		}
		entry.paused = true
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
	entry.paused, entry.judging = false, false
	p.mu.Unlock()
	d.settleTurnPush(sessionID, settledOnIdle)
}

func (d *Daemon) dropTurnPush(sessionID, turnID, reason, event string) {
	p := d.turnPushes
	p.mu.Lock()
	entry := p.pending[sessionID]
	if entry == nil || turnID != "" && entry.turnID != turnID {
		p.mu.Unlock()
		return
	}
	p.mu.Unlock()
	if !p.take(sessionID, entry) {
		return
	}
	if event != "" {
		d.log.Printf("push settled session=%s turns=%s reason=%s event=%s", sessionID, strings.Join(entry.turns, ","), reason, event)
	}
}

func (d *Daemon) turnVerdictLanded(sessionID, turnID string) {
	p := d.turnPushes
	p.mu.Lock()
	entry := p.pending[sessionID]
	if entry == nil || entry.paused || entry.turnID != turnID {
		p.mu.Unlock()
		return
	}
	entry.judging = false
	p.mu.Unlock()
	d.settleTurnPush(sessionID, settledOnVerdict)
}

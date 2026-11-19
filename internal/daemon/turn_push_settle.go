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
	settledOnShutdown = "shutdown"
)

const (
	verdictQueued = iota + 1
	verdictDone
	verdictNeedsYou
)

type pendingTurnPush struct {
	turnID string
	turns  []string
	notice turnCompletedNotice
	paused bool
	gen    int
	timer  *time.Timer
}

func (entry *pendingTurnPush) pause() {
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.paused = true
	entry.gen++
}

type turnPushes struct {
	mu       sync.Mutex
	window   time.Duration
	pending  map[string]*pendingTurnPush
	verdicts map[string]map[string]int
	sending  map[string]int
	expected map[string]int
	injected map[string]int
	flushed  map[string]string
}

func newTurnPushes() *turnPushes {
	return &turnPushes{window: turnPushSettle, pending: map[string]*pendingTurnPush{}, verdicts: map[string]map[string]int{}, sending: map[string]int{}, expected: map[string]int{}, injected: map[string]int{}, flushed: map[string]string{}}
}

func (p *turnPushes) mark(sessionID, turnID string, state int) {
	marks := p.verdicts[sessionID]
	if marks == nil {
		marks = map[string]int{}
		p.verdicts[sessionID] = marks
	}
	marks[turnID] = state
}

func (p *turnPushes) verdict(sessionID, turnID string) int {
	return p.verdicts[sessionID][turnID]
}

func (p *turnPushes) remove(sessionID string, entry *pendingTurnPush) {
	delete(p.pending, sessionID)
	if entry.timer != nil {
		entry.timer.Stop()
	}
	entry.gen++
}

func (d *Daemon) turnJudgmentQueued(sessionID, turnID string) {
	p := d.turnPushes
	p.mu.Lock()
	p.mark(sessionID, turnID, verdictQueued)
	p.mu.Unlock()
}

// A session pushes once per burst of turns: a turn's finished push waits for its verdict, or for the session to go idle, and a newer turn of the session takes its place.
func (d *Daemon) scheduleTurnPush(sessionID, turnID string, notice turnCompletedNotice) {
	p := d.turnPushes
	entry := &pendingTurnPush{turnID: turnID, notice: notice}
	p.mu.Lock()
	answered := p.verdict(sessionID, turnID) == verdictNeedsYou
	for marked, state := range p.verdicts[sessionID] {
		if marked != turnID && state != verdictQueued {
			delete(p.verdicts[sessionID], marked)
		}
	}
	if prior := p.pending[sessionID]; prior != nil {
		p.remove(sessionID, prior)
		entry.turns = append(entry.turns, prior.turns...)
	}
	entry.turns = append(entry.turns, turnID)
	if answered {
		p.mu.Unlock()
		return
	}
	p.pending[sessionID] = entry
	gen := entry.gen
	p.mu.Unlock()
	d.settleTurnPush(sessionID, entry, gen, settledOnIdle)
}

// A delivery records its turn running before it takes its message off the queue, so the queue is read first: a message gone from it has its turn on record.
func (d *Daemon) settleTurnPush(sessionID string, entry *pendingTurnPush, gen int, reason string) {
	p := d.turnPushes
	p.mu.Lock()
	if p.pending[sessionID] != entry || entry.paused || entry.gen != gen {
		p.mu.Unlock()
		return
	}
	queued := d.Registry.QueueCount(sessionID) > 0
	if d.anotherTurnRunning(sessionID, entry.turnID) {
		entry.pause()
		p.mu.Unlock()
		return
	}
	if reason != settledOnDeadline && (queued || p.sending[sessionID] > 0 || p.expected[sessionID] > 0 || p.verdict(sessionID, entry.turnID) == verdictQueued) {
		if entry.timer == nil {
			armed := entry.gen
			entry.timer = time.AfterFunc(p.window, func() {
				d.background.start(func(context.Context) { d.settleTurnPush(sessionID, entry, armed, settledOnDeadline) })
			})
		}
		p.mu.Unlock()
		return
	}
	p.remove(sessionID, entry)
	if reason == settledOnDeadline {
		delete(p.expected, sessionID)
	}
	p.mu.Unlock()
	d.sendSettledTurnPush(sessionID, entry, reason)
}

func (d *Daemon) anotherTurnRunning(sessionID, turnID string) bool {
	runtime, found, err := d.Registry.RuntimeState(sessionID)
	if err != nil {
		d.log.Printf("read the runtime of %s to settle its push: %s", d.resolveDisplay(sessionID), err)
		return false
	}
	return found && runtime.LastStatus == surface.StatusBusy && runtime.ActiveTurnID != "" && runtime.ActiveTurnID != turnID
}

func (d *Daemon) sendSettledTurnPush(sessionID string, entry *pendingTurnPush, reason string) {
	d.logSettledPush(sessionID, entry.turns, reason, "turn.completed")
	d.recordTurnNotice(sessionID, entry.turnID, entry.notice)
	d.notifyTurnCompleted(entry.notice, sessionID, entry.turnID)
}

// A stopping daemon sends what it still holds, each under its own deadline, since the stop cancels background work and a restarted daemon does not see the turn end again; a needs-you verdict for a turn it flushed is not sent after it.
func (d *Daemon) flushTurnPushes() {
	p := d.turnPushes
	p.mu.Lock()
	held := make(map[string]*pendingTurnPush, len(p.pending))
	for sessionID, entry := range p.pending {
		p.remove(sessionID, entry)
		p.flushed[sessionID] = entry.turnID
		held[sessionID] = entry
	}
	p.mu.Unlock()
	var sends sync.WaitGroup
	for sessionID, entry := range held {
		d.logSettledPush(sessionID, entry.turns, settledOnShutdown, "turn.completed")
		d.recordTurnNotice(sessionID, entry.turnID, entry.notice)
		sends.Add(1)
		go func() {
			defer sends.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d.deliverTurnCompleted(ctx, entry.notice, sessionID, entry.turnID)
		}()
	}
	sends.Wait()
}

// injected counts messages that reached the session mid-turn, which this does not tie to a later turn, so the log shows how often a burst may still push twice.
func (d *Daemon) logSettledPush(sessionID string, turns []string, reason, event string) {
	p := d.turnPushes
	p.mu.Lock()
	injected := p.injected[sessionID]
	delete(p.injected, sessionID)
	p.mu.Unlock()
	d.log.Printf("push settled session=%s turns=%s reason=%s event=%s injected=%d", sessionID, strings.Join(turns, ","), reason, event, injected)
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

// A send counts only while a finished turn's push waits, so every turn that ends after it is one it could have started.
func (d *Daemon) turnEndedForPush(sessionID string) {
	p := d.turnPushes
	p.mu.Lock()
	defer p.mu.Unlock()
	p.meetExpected(sessionID)
}

func (p *turnPushes) meetExpected(sessionID string) {
	if p.expected[sessionID]--; p.expected[sessionID] <= 0 {
		delete(p.expected, sessionID)
	}
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
	if entry == nil {
		p.mu.Unlock()
		return
	}
	if entry.timer != nil {
		entry.timer.Stop()
		entry.timer = nil
	}
	entry.paused = false
	entry.gen++
	gen := entry.gen
	p.mu.Unlock()
	d.settleTurnPush(sessionID, entry, gen, settledOnIdle)
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
	if p.verdict(sessionID, turnID) == verdictQueued {
		p.mark(sessionID, turnID, verdictDone)
	}
	entry := p.pending[sessionID]
	if entry == nil || entry.turnID != turnID {
		p.mu.Unlock()
		return
	}
	gen := entry.gen
	p.mu.Unlock()
	d.settleTurnPush(sessionID, entry, gen, settledOnVerdict)
}

// The needs-you push stands for the turn's finished push, and for the burst it ends.
func (d *Daemon) needsYouTakesTurnPush(sessionID, turnID string) (*pendingTurnPush, bool) {
	p := d.turnPushes
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.flushed[sessionID] == turnID {
		return nil, false
	}
	p.mark(sessionID, turnID, verdictNeedsYou)
	if entry := p.pending[sessionID]; entry != nil && entry.turnID == turnID {
		p.remove(sessionID, entry)
		return entry, true
	}
	return nil, true
}

func (d *Daemon) needsYouNotPushed(sessionID, turnID string, taken *pendingTurnPush) {
	p := d.turnPushes
	p.mu.Lock()
	defer p.mu.Unlock()
	p.mark(sessionID, turnID, verdictDone)
	if taken != nil && p.pending[sessionID] == nil {
		taken.gen++
		p.pending[sessionID] = taken
	}
}

func (d *Daemon) turnPushHandoff(sessionID string) func(accepted bool) {
	p := d.turnPushes
	runtime, found, _ := d.Registry.RuntimeState(sessionID)
	midTurn := found && runtime.LastStatus == surface.StatusBusy
	p.mu.Lock()
	if p.pending[sessionID] == nil {
		p.mu.Unlock()
		return func(accepted bool) {
			if accepted && midTurn {
				p.mu.Lock()
				p.injected[sessionID]++
				p.mu.Unlock()
			}
		}
	}
	p.sending[sessionID]++
	p.mu.Unlock()
	return func(accepted bool) {
		p.mu.Lock()
		if p.sending[sessionID]--; p.sending[sessionID] <= 0 {
			delete(p.sending, sessionID)
		}
		entry := p.pending[sessionID]
		if accepted && entry != nil {
			p.expected[sessionID]++
		}
		if entry == nil || entry.paused {
			p.mu.Unlock()
			return
		}
		if accepted {
			if entry.timer != nil {
				entry.timer.Stop()
				entry.timer = nil
			}
			entry.gen++
		}
		gen := entry.gen
		p.mu.Unlock()
		d.settleTurnPush(sessionID, entry, gen, settledOnIdle)
	}
}

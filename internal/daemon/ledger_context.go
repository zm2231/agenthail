package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	contextWait     = 20 * time.Second
	contextPoll     = 200 * time.Millisecond
	contextWorkers  = 8
	contextRows     = 400
	contextPage     = 200
	contextRecovery = 24 * time.Hour
)

type contextJob struct {
	key         string
	utteranceID string
	targetID    string
	seq         int64
	at          time.Time
}

type messageContexts struct {
	wait    time.Duration
	poll    time.Duration
	slots   chan struct{}
	mu      sync.Mutex
	ctx     context.Context
	stopped bool
	running sync.WaitGroup
}

func newMessageContexts() *messageContexts {
	return &messageContexts{wait: contextWait, poll: contextPoll, slots: make(chan struct{}, contextWorkers)}
}

func (d *Daemon) queueContext(job contextJob) {
	c := d.contexts
	c.mu.Lock()
	ctx, open := c.ctx, c.ctx != nil && !c.stopped
	if open {
		c.running.Add(1)
	}
	c.mu.Unlock()
	if !open {
		return
	}
	go func() {
		defer c.running.Done()
		select {
		case c.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		defer func() { <-c.slots }()
		d.recordContext(ctx, job)
	}()
}

func (d *Daemon) startMessageContexts(ctx context.Context) <-chan struct{} {
	c := d.contexts
	c.mu.Lock()
	c.ctx = ctx
	c.running.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.running.Done()
		d.recoverContexts(ctx)
	}()
	done := make(chan struct{})
	go func() {
		<-ctx.Done()
		c.mu.Lock()
		c.stopped = true
		c.mu.Unlock()
		c.running.Wait()
		close(done)
	}()
	return done
}

func (d *Daemon) recoverContexts(ctx context.Context) {
	store := d.ledgerStore()
	floor := time.Now().Add(-contextRecovery)
	keys, err := store.LocalCaptureProjects(floor)
	if err != nil {
		d.log.Printf("ledger context: list projects at start: %s", err)
		return
	}
	var owed []contextJob
	for _, key := range keys {
		refs, err := store.LocalWithoutContextSince(key, floor)
		if err != nil {
			d.log.Printf("ledger context: list messages at start in %s: %s", key, err)
			continue
		}
		for _, o := range refs {
			owed = append(owed, contextJob{key: key, utteranceID: o.Ref, targetID: o.TargetID, seq: o.Seq, at: o.At})
		}
	}
	slices.SortFunc(owed, func(a, b contextJob) int { return cmp.Compare(a.seq, b.seq) })
	for _, job := range owed {
		if ctx.Err() != nil {
			return
		}
		d.recordContext(ctx, job)
	}
}

func (d *Daemon) recordContext(ctx context.Context, job contextJob) {
	payload, ok := d.messageContext(ctx, job)
	if !ok {
		return
	}
	inserted, err := d.ledgerStore().RecordContext(job.key, payload, time.Now())
	if err != nil {
		d.log.Printf("ledger context: record %s in %s: %s", job.utteranceID, job.key, err)
		return
	}
	if inserted {
		d.queueSort(job.key, job.utteranceID, job.seq, job.at)
	}
}

// messageContext waits until the capture time plus the bound for the session's own entry for the message.
func (d *Daemon) messageContext(ctx context.Context, job contextJob) (ledger.ContextPayload, bool) {
	deadline := job.at.Add(d.contexts.wait)
	if time.Now().Before(deadline) {
		if release := d.holdContextSource(ctx, job.targetID); release != nil {
			defer release()
		}
	}
	match := d.userEntryMatcher(job)
	for {
		if reply, found := d.answeredReply(job.targetID, match); found {
			status := ledger.ReplyNone
			if reply != nil {
				status = ledger.ReplyFound
			}
			return ledger.ContextPayload{UtteranceID: job.utteranceID, Status: status, JoinedBy: ledger.JoinedByID, Reply: reply}, true
		}
		if !time.Now().Before(deadline) {
			late := ledger.ContextPayload{UtteranceID: job.utteranceID, Status: ledger.ReplyLate}
			if reply := d.replyBefore(job.targetID, job.at); reply != nil {
				late.JoinedBy, late.Reply = ledger.JoinedByTime, reply
			}
			return late, true
		}
		select {
		case <-ctx.Done():
			return ledger.ContextPayload{}, false
		case <-time.After(min(d.contexts.poll, time.Until(deadline))):
		}
	}
}

func (d *Daemon) holdContextSource(ctx context.Context, sessionID string) func() {
	session, err := d.Registry.Session(sessionID)
	if err != nil || session == nil {
		return nil
	}
	adapter, err := d.adapters().For(session)
	if err != nil || !surface.EffectiveCapabilities(adapter, session).Stream {
		return nil
	}
	_, release, err := d.sources.holdSource(ctx, session, adapter, "ledger-context")
	if err != nil {
		return nil
	}
	return release
}

func (d *Daemon) userEntryMatcher(job contextJob) func(sessionJournalPayload) bool {
	if promptID, ok := strings.CutPrefix(job.utteranceID, ledger.PromptRef("")); ok {
		return func(p sessionJournalPayload) bool { return p.PromptID == promptID }
	}
	uuid, ok := strings.CutPrefix(job.utteranceID, ledger.DeliveryRef(""))
	if !ok {
		return func(sessionJournalPayload) bool { return false }
	}
	return func(p sessionJournalPayload) bool {
		if p.MessageID == uuid {
			return true
		}
		promptID := d.delivered.promptFor(job.targetID, uuid)
		return promptID != "" && p.PromptID == promptID
	}
}

// answeredReply finds the session's entry for the message among its newest entries and returns
// the last reply before it; found is false while that entry is not there.
func (d *Daemon) answeredReply(sessionID string, match func(sessionJournalPayload) bool) (reply *ledger.Reply, found bool) {
	var cursor uint64
	for rows := 0; ; {
		page, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, contextPage)
		if err != nil {
			return nil, found
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			var p sessionJournalPayload
			if json.Unmarshal(page.Entries[i].Payload, &p) != nil {
				continue
			}
			if !found {
				found = p.Role == "user" && p.Kind == "message" && match(p)
				continue
			}
			if reply := d.journalReply(sessionID, p); reply != nil {
				return reply, true
			}
		}
		rows += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 || (!found && rows >= contextRows) {
			return nil, found
		}
		cursor = page.NextBefore
	}
}

func (d *Daemon) replyBefore(sessionID string, before time.Time) *ledger.Reply {
	var cursor uint64
	for rows := 0; rows < contextRows; {
		page, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, contextPage)
		if err != nil {
			return nil
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			var p sessionJournalPayload
			if json.Unmarshal(page.Entries[i].Payload, &p) != nil {
				continue
			}
			if ts, err := time.Parse(time.RFC3339Nano, p.TS); err != nil || !ts.Before(before) {
				continue
			}
			if reply := d.journalReply(sessionID, p); reply != nil {
				return reply
			}
		}
		rows += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 {
			return nil
		}
		cursor = page.NextBefore
	}
	return nil
}

func (d *Daemon) journalReply(sessionID string, p sessionJournalPayload) *ledger.Reply {
	if p.Role != "assistant" || p.Body == "" || (p.Kind != "message" && p.Kind != "text") {
		return nil
	}
	body := p.Body
	if p.Truncated && p.BodyRef != "" {
		if full, _, err := d.Registry.SessionJournalBody(sessionID, p.BodyRef, 0, 1<<40); err == nil {
			body = string(full)
		}
	}
	return ledger.NewReply(p.ItemID, p.TS, body)
}

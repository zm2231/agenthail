package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/goalsort"
	"github.com/zm2231/agenthail/internal/ledger"
)

const (
	sorterQueue      = 64
	sorterDeadline   = 10 * time.Second
	sorterRecovery   = 24 * time.Hour
	sorterReplyRows  = 400
	sorterReplyPage  = 200
	sorterReplyBytes = 64 * 1024
)

type sortJob struct {
	key         string
	utteranceID string
	at          time.Time
}

type ledgerSorter struct {
	jobs chan sortJob
	http *http.Client
	now  func() time.Time

	mu        sync.Mutex
	since     time.Time
	lastError string
	failures  int
	dropped   int
	configErr string
	backlog   map[string]time.Time
}

func newLedgerSorter() *ledgerSorter {
	return &ledgerSorter{jobs: make(chan sortJob, sorterQueue), now: time.Now, backlog: map[string]time.Time{}}
}

func (d *Daemon) queueSort(key, utteranceID string, at time.Time) {
	s := d.sorter
	if s == nil {
		return
	}
	select {
	case s.jobs <- sortJob{key: key, utteranceID: utteranceID, at: at}:
	default:
		s.mu.Lock()
		s.dropped++
		s.owe(key, at)
		s.mu.Unlock()
	}
}

func (d *Daemon) runLedgerSorter(ctx context.Context) {
	s := d.sorter
	if s == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			d.sortQueued(ctx, job)
		}
	}
}

func (d *Daemon) sortQueued(ctx context.Context, job sortJob) {
	config, ok := d.sorterConfig()
	if !ok {
		return
	}
	if err := d.sortOne(ctx, config, job); err != nil {
		d.sorter.failed(d, job, err)
		return
	}
	owed := d.sorter.succeeded(d)
	for i, project := range owed {
		if ctx.Err() != nil || !d.resort(ctx, config, project) {
			d.sorter.mu.Lock()
			for _, left := range owed[i:] {
				d.sorter.owe(left.key, left.since)
			}
			d.sorter.mu.Unlock()
			return
		}
	}
}

func (d *Daemon) sorterConfig() (goalsort.Config, bool) {
	home, err := os.UserHomeDir()
	if err != nil {
		return goalsort.Config{}, false
	}
	config, ok, err := goalsort.LoadConfig(home)
	s := d.sorter
	s.mu.Lock()
	defer s.mu.Unlock()
	message := ""
	if err != nil {
		message = err.Error()
	}
	if message != s.configErr && message != "" {
		d.log.Printf("ledger sorter: %s", message)
	}
	s.configErr = message
	return config, ok
}

type owedSorts struct {
	key   string
	since time.Time
}

func (d *Daemon) resort(ctx context.Context, config goalsort.Config, owed owedSorts) bool {
	since := owed.since
	if floor := d.sorter.now().Add(-sorterRecovery); since.Before(floor) {
		since = floor
	}
	refs, err := d.ledgerStore().LocalProvisionalSince(owed.key, since.Truncate(time.Second))
	if err != nil {
		d.log.Printf("ledger sorter: list messages to sort again in %s: %s", owed.key, err)
		return true
	}
	for _, ref := range refs {
		if ctx.Err() != nil {
			d.sorter.mu.Lock()
			d.sorter.owe(owed.key, since)
			d.sorter.mu.Unlock()
			return false
		}
		if err := d.sortOne(ctx, config, sortJob{key: owed.key, utteranceID: ref}); err != nil {
			d.sorter.failed(d, sortJob{key: owed.key, utteranceID: ref, at: since}, err)
			return false
		}
	}
	return true
}

// sortOne returns an error only when the sorter could not answer; a ledger problem is logged and leaves the message provisional.
func (d *Daemon) sortOne(ctx context.Context, config goalsort.Config, job sortJob) error {
	store := d.ledgerStore()
	input, ok, err := store.SortInputFor(job.key, job.utteranceID)
	if err != nil {
		d.log.Printf("ledger sorter: read %s in %s: %s", job.utteranceID, job.key, err)
		return nil
	}
	if !ok {
		return nil
	}
	spoken := job.at
	if spoken.IsZero() {
		spoken = input.At
	}
	question := goalsort.Question{Message: input.Text, PreviousReply: d.previousReply(input.TargetID, spoken)}
	for _, g := range input.Goals {
		question.Goals = append(question.Goals, goalsort.Goal{ID: g.ID, Name: g.Name, Requests: g.Requests})
	}
	asked := time.Now()
	callCtx, cancel := context.WithTimeout(ctx, sorterDeadline)
	defer cancel()
	answer, err := goalsort.Client{Config: config, HTTP: d.sorter.http}.Ask(callCtx, question)
	if err != nil {
		return err
	}
	if answer.Confidence < ledger.SortConfidence || !slices.ContainsFunc(input.Goals, func(g ledger.SortGoal) bool { return g.ID == answer.Choice }) {
		return nil
	}
	payload := ledger.SortedPayload{UtteranceID: job.utteranceID, GoalID: answer.Choice, Confidence: answer.Confidence, Model: config.Model, Ms: time.Since(asked).Milliseconds()}
	if _, err := store.RecordSort(job.key, payload, time.Now()); err != nil {
		d.log.Printf("ledger sorter: file %s in %s: %s", job.utteranceID, job.key, err)
	}
	return nil
}

func (d *Daemon) previousReply(sessionID string, before time.Time) string {
	var cursor uint64
	for rows := 0; rows < sorterReplyRows; {
		page, err := d.Registry.ReadSessionJournalPage(sessionID, cursor, sorterReplyPage)
		if err != nil {
			return ""
		}
		for i := len(page.Entries) - 1; i >= 0; i-- {
			var p sessionJournalPayload
			if json.Unmarshal(page.Entries[i].Payload, &p) != nil || p.Role != "assistant" || p.Body == "" || (p.Kind != "message" && p.Kind != "text") {
				continue
			}
			ts, err := time.Parse(time.RFC3339Nano, p.TS)
			if err != nil || !ts.Before(before) {
				continue
			}
			if p.Truncated && p.BodyRef != "" {
				if full, _, err := d.Registry.SessionJournalBody(sessionID, p.BodyRef, 0, sorterReplyBytes); err == nil {
					return string(full)
				}
			}
			return p.Body
		}
		rows += len(page.Entries)
		if page.NextBefore == 0 || len(page.Entries) == 0 {
			return ""
		}
		cursor = page.NextBefore
	}
	return ""
}

func (s *ledgerSorter) owe(key string, at time.Time) {
	if since, ok := s.backlog[key]; !ok || at.Before(since) {
		s.backlog[key] = at
	}
}

func (s *ledgerSorter) failed(d *Daemon, job sortJob, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures == 0 {
		s.since = s.now()
		d.log.Printf("ledger sorter unavailable: %s", err)
	}
	s.failures++
	s.lastError = err.Error()
	at := job.at
	if at.IsZero() {
		at = s.now()
	}
	s.owe(job.key, at)
}

func (s *ledgerSorter) succeeded(d *Daemon) []owedSorts {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		d.log.Printf("ledger sorter answered again after %d failures since %s", s.failures, s.since.Format(time.RFC3339))
	}
	s.failures, s.lastError, s.since = 0, "", time.Time{}
	owed := make([]owedSorts, 0, len(s.backlog))
	for key, since := range s.backlog {
		owed = append(owed, owedSorts{key: key, since: since})
	}
	clear(s.backlog)
	slices.SortFunc(owed, func(a, b owedSorts) int { return a.since.Compare(b.since) })
	return owed
}

func (d *Daemon) sorterHealth() *ledger.SorterHealth {
	s := d.sorter
	if s == nil {
		return nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	config, ok, err := goalsort.LoadConfig(home)
	if err != nil {
		return &ledger.SorterHealth{Error: err.Error()}
	}
	if !ok {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return &ledger.SorterHealth{Model: config.Model, Since: s.since, Error: s.lastError, Failures: s.failures, Dropped: s.dropped}
}

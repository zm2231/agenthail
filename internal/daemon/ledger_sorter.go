package daemon

import (
	"cmp"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"slices"
	"strings"
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
	sorterRetry      = 5 * time.Minute
)

type sortJob struct {
	key         string
	utteranceID string
	seq         int64
	at          time.Time
}

type ledgerSorter struct {
	jobs  chan sortJob
	http  *http.Client
	now   func() time.Time
	retry time.Duration

	mu        sync.Mutex
	since     time.Time
	lastError string
	failures  int
	dropped   int
	configErr string
	backlog   map[string]int64
}

func newLedgerSorter() *ledgerSorter {
	return &ledgerSorter{jobs: make(chan sortJob, sorterQueue), now: time.Now, retry: sorterRetry, backlog: map[string]int64{}}
}

func (d *Daemon) queueSort(key, utteranceID string, seq int64, at time.Time) {
	s := d.sorter
	if s == nil {
		return
	}
	select {
	case s.jobs <- sortJob{key: key, utteranceID: utteranceID, seq: seq, at: at}:
	default:
		s.mu.Lock()
		s.dropped++
		s.owe(key, seq)
		s.mu.Unlock()
	}
}

func (d *Daemon) startLedgerSorter(ctx context.Context) <-chan struct{} {
	done := make(chan struct{})
	if d.sorter == nil {
		close(done)
		return done
	}
	d.oweRecentCaptures()
	go func() {
		defer close(done)
		d.runLedgerSorter(ctx)
	}()
	return done
}

func (d *Daemon) runLedgerSorter(ctx context.Context) {
	s := d.sorter
	retry := time.NewTicker(s.retry)
	defer retry.Stop()
	d.sortOwed(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			d.sortQueued(ctx, job)
		case <-retry.C:
			d.sortOwed(ctx)
		}
	}
}

// The log, not memory, says what is owed after a restart: every provisional message this host captured in the last day.
func (d *Daemon) oweRecentCaptures() {
	keys, err := d.ledgerStore().LocalCaptureProjects(d.sorter.now().Add(-sorterRecovery))
	if err != nil {
		d.log.Printf("ledger sorter: list projects to sort at start: %s", err)
		return
	}
	d.sorter.mu.Lock()
	defer d.sorter.mu.Unlock()
	for _, key := range keys {
		d.sorter.owe(key, 0)
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
	d.sorter.answered(d)
	d.sortOwedWith(ctx, config)
}

func (d *Daemon) sortOwed(ctx context.Context) {
	d.sorter.mu.Lock()
	empty := len(d.sorter.backlog) == 0
	d.sorter.mu.Unlock()
	if empty {
		return
	}
	if config, ok := d.sorterConfig(); ok {
		d.sortOwedWith(ctx, config)
	}
}

func (d *Daemon) sortOwedWith(ctx context.Context, config goalsort.Config) {
	var jobs []sortJob
	for _, project := range d.sorter.takeOwed() {
		refs, err := d.ledgerStore().LocalProvisionalFrom(project.key, project.fromSeq, d.sorter.now().Add(-sorterRecovery))
		if err != nil {
			d.log.Printf("ledger sorter: list messages to sort again in %s: %s", project.key, err)
			continue
		}
		for _, ref := range refs {
			jobs = append(jobs, sortJob{key: project.key, utteranceID: ref.Ref, seq: ref.Seq})
		}
	}
	slices.SortFunc(jobs, func(a, b sortJob) int { return cmp.Compare(a.seq, b.seq) })
	for i, job := range jobs {
		if ctx.Err() != nil {
			d.sorter.oweAll(jobs[i:])
			return
		}
		if err := d.sortOne(ctx, config, job); err != nil {
			d.sorter.failed(d, job, err)
			d.sorter.oweAll(jobs[i+1:])
			return
		}
		d.sorter.answered(d)
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
	key     string
	fromSeq int64
}

func (s *ledgerSorter) oweAll(jobs []sortJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, job := range jobs {
		s.owe(job.key, job.seq)
	}
}

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

func (s *ledgerSorter) owe(key string, seq int64) {
	if from, ok := s.backlog[key]; !ok || seq < from {
		s.backlog[key] = seq
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
	s.owe(job.key, job.seq)
}

func (s *ledgerSorter) answered(d *Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		d.log.Printf("ledger sorter answered again after %d failures since %s", s.failures, s.since.Format(time.RFC3339))
	}
	s.failures, s.lastError, s.since = 0, "", time.Time{}
}

func (s *ledgerSorter) takeOwed() []owedSorts {
	s.mu.Lock()
	defer s.mu.Unlock()
	owed := make([]owedSorts, 0, len(s.backlog))
	for key, from := range s.backlog {
		owed = append(owed, owedSorts{key: key, fromSeq: from})
	}
	clear(s.backlog)
	slices.SortFunc(owed, func(a, b owedSorts) int { return strings.Compare(a.key, b.key) })
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

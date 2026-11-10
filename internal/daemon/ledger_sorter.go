package daemon

import (
	"cmp"
	"context"
	"maps"
	"net/http"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/goalsort"
	"github.com/zm2231/agenthail/internal/ledger"
)

const (
	sorterQueue    = 64
	sorterDeadline = 10 * time.Second
	sorterRecovery = 24 * time.Hour
	sorterRetry    = 5 * time.Minute
)

type sortJob struct {
	key         string
	utteranceID string
	seq         int64
	at          time.Time
}

type owedSort struct {
	key         string
	utteranceID string
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
	owed      map[owedSort]sortJob
}

func newLedgerSorter() *ledgerSorter {
	return &ledgerSorter{jobs: make(chan sortJob, sorterQueue), now: time.Now, retry: sorterRetry, owed: map[owedSort]sortJob{}}
}

func (d *Daemon) queueSort(key, utteranceID string, seq int64, at time.Time) {
	s := d.sorter
	if s == nil {
		return
	}
	job := sortJob{key: key, utteranceID: utteranceID, seq: seq, at: at}
	select {
	case s.jobs <- job:
	default:
		s.mu.Lock()
		s.dropped++
		s.owe(job)
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
	store := d.ledgerStore()
	floor := d.sorter.now().Add(-sorterRecovery)
	keys, err := store.LocalCaptureProjects(floor)
	if err != nil {
		d.log.Printf("ledger sorter: list projects to sort at start: %s", err)
		return
	}
	for _, key := range keys {
		refs, err := store.LocalProvisionalSince(key, floor)
		if err != nil {
			d.log.Printf("ledger sorter: list messages to sort at start in %s: %s", key, err)
			continue
		}
		d.sorter.mu.Lock()
		for _, ref := range refs {
			d.sorter.owe(sortJob{key: key, utteranceID: ref.Ref, seq: ref.Seq, at: ref.At})
		}
		d.sorter.mu.Unlock()
	}
}

func (d *Daemon) sortQueued(ctx context.Context, job sortJob) {
	config, ok := d.sorterConfig()
	if !ok || d.sorter.isOwed(job) {
		return
	}
	if !d.sortAndSettle(ctx, config, job) {
		return
	}
	d.sortOwedWith(ctx, config)
}

func (d *Daemon) sortOwed(ctx context.Context) {
	if len(d.sorter.owedJobs()) == 0 {
		return
	}
	if config, ok := d.sorterConfig(); ok {
		d.sortOwedWith(ctx, config)
	}
}

func (d *Daemon) sortOwedWith(ctx context.Context, config goalsort.Config) {
	floor := d.sorter.now().Add(-sorterRecovery)
	for _, job := range d.sorter.owedJobs() {
		if ctx.Err() != nil {
			return
		}
		if job.at.Before(floor) {
			d.sorter.settle(job)
			continue
		}
		if !d.sortAndSettle(ctx, config, job) {
			return
		}
	}
}

func (d *Daemon) sortAndSettle(ctx context.Context, config goalsort.Config, job sortJob) bool {
	if err := d.sortOne(ctx, config, job); err != nil {
		d.sorter.failed(d, job, err)
		return false
	}
	d.sorter.answered(d)
	d.sorter.settle(job)
	return true
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
	question := goalsort.Question{Message: ledger.Marked(input.Text, input.Context.Quotes)}
	if input.Context.Reply != nil {
		question.PreviousReply = input.Context.Reply.Text
	}
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
	took := time.Since(asked).Milliseconds()
	if answer.Confidence < ledger.SortConfidence || !slices.ContainsFunc(input.Goals, func(g ledger.SortGoal) bool { return g.ID == answer.Choice }) {
		unfiled := ledger.UnfiledPayload{UtteranceID: job.utteranceID, Choice: answer.Choice, Confidence: answer.Confidence, Model: config.Model, Ms: took}
		if _, err := store.RecordUnfiled(job.key, unfiled, time.Now()); err != nil {
			d.log.Printf("ledger sorter: keep the answer for %s in %s: %s", job.utteranceID, job.key, err)
		}
		return nil
	}
	payload := ledger.SortedPayload{UtteranceID: job.utteranceID, GoalID: answer.Choice, Confidence: answer.Confidence, Model: config.Model, Ms: took}
	if _, err := store.RecordSort(job.key, payload, time.Now()); err != nil {
		d.log.Printf("ledger sorter: file %s in %s: %s", job.utteranceID, job.key, err)
	}
	return nil
}

func (s *ledgerSorter) owe(job sortJob) {
	s.owed[owedSort{key: job.key, utteranceID: job.utteranceID}] = job
}

func (s *ledgerSorter) isOwed(job sortJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, owed := s.owed[owedSort{key: job.key, utteranceID: job.utteranceID}]
	return owed
}

func (s *ledgerSorter) settle(job sortJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.owed, owedSort{key: job.key, utteranceID: job.utteranceID})
}

func (s *ledgerSorter) owedJobs() []sortJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.SortedFunc(maps.Values(s.owed), func(a, b sortJob) int { return cmp.Compare(a.seq, b.seq) })
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
	s.owe(job)
}

func (s *ledgerSorter) answered(d *Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failures > 0 {
		d.log.Printf("ledger sorter answered again after %d failures since %s", s.failures, s.since.Format(time.RFC3339))
	}
	s.failures, s.lastError, s.since = 0, "", time.Time{}
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

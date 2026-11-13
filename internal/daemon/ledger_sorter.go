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
	// contextLanding covers the whole second a capture time is stored to and the write after a context's wait.
	contextLanding = 2 * time.Second
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
	held      map[owedSort]sortJob
	wake      time.Time
}

func newLedgerSorter() *ledgerSorter {
	return &ledgerSorter{jobs: make(chan sortJob, sorterQueue), now: time.Now, retry: sorterRetry, owed: map[owedSort]sortJob{}, held: map[owedSort]sortJob{}}
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
		s.wakeAt(s.now())
	}
}

// The sorter starts after recovery has recorded the contexts owed at start, which it does oldest first, so an older
// message is never asked about after a newer one whose context was already there.
func (d *Daemon) startLedgerSorter(ctx context.Context, contextsRecovered <-chan struct{}) <-chan struct{} {
	done := make(chan struct{})
	if d.sorter == nil {
		close(done)
		return done
	}
	d.oweRecentCaptures()
	go func() {
		defer close(done)
		select {
		case <-contextsRecovered:
		case <-ctx.Done():
			return
		}
		d.runLedgerSorter(ctx)
	}()
	return done
}

func (d *Daemon) runLedgerSorter(ctx context.Context) {
	s := d.sorter
	retry := time.NewTicker(s.retry)
	defer retry.Stop()
	wake := time.NewTimer(time.Hour)
	wake.Stop()
	var armed time.Time
	d.sortOwed(ctx)
	for {
		if next := s.takeWake(); !next.IsZero() && (armed.IsZero() || next.Before(armed)) {
			armed = next
			wake.Reset(time.Until(next))
		}
		select {
		case <-ctx.Done():
			return
		case job := <-s.jobs:
			d.sortQueued(ctx, job)
		case <-retry.C:
			d.sortWaiting(ctx)
		case <-wake.C:
			armed = time.Time{}
			d.sortWaiting(ctx)
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
	if !ok || d.sorter.isOwed(job) || d.sorter.isHeld(job) {
		return
	}
	if until, held := d.heldBehind(job); held {
		d.sorter.hold(job, until)
		return
	}
	if !d.sortAndSettle(ctx, config, job) {
		return
	}
	d.releaseHeld(ctx, config)
	d.sortOwedWith(ctx, config)
}

func (d *Daemon) sortWaiting(ctx context.Context) {
	if len(d.sorter.heldJobs()) == 0 && len(d.sorter.owedJobs()) == 0 {
		return
	}
	if config, ok := d.sorterConfig(); ok {
		d.releaseHeld(ctx, config)
		d.sortOwedWith(ctx, config)
	}
}

func (d *Daemon) releaseHeld(ctx context.Context, config goalsort.Config) {
	for _, job := range d.sorter.heldJobs() {
		if ctx.Err() != nil {
			return
		}
		if until, held := d.heldBehind(job); held {
			d.sorter.hold(job, until)
			continue
		}
		d.sorter.release(job)
		if !d.sortAndSettle(ctx, config, job) {
			return
		}
	}
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
		if until, held := d.heldBehind(job); held {
			d.sorter.wakeAt(until)
			continue
		}
		if !d.sortAndSettle(ctx, config, job) {
			return
		}
	}
}

// heldBehind keeps a project's messages asked about in the order they were captured: a message waits while an
// earlier one in its project, not yet asked about, is still within its context's wait, until the first such wait ends.
// A message already owed is not waited on: the owed pass asks in order, and one whose ask failed is retried later.
func (d *Daemon) heldBehind(job sortJob) (time.Time, bool) {
	window := d.contexts.wait + contextLanding
	now := d.sorter.now()
	pending, err := d.ledgerStore().LocalProvisionalSince(job.key, now.Add(-window))
	if err != nil {
		d.log.Printf("ledger sorter: list earlier messages to sort in %s: %s", job.key, err)
		return time.Time{}, false
	}
	var until time.Time
	for _, earlier := range pending {
		if earlier.Seq >= job.seq {
			break
		}
		if d.sorter.isOwed(sortJob{key: job.key, utteranceID: earlier.Ref}) {
			continue
		}
		if end := earlier.At.Add(window); now.Before(end) && (until.IsZero() || end.Before(until)) {
			until = end
		}
	}
	return until, !until.IsZero()
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

func (s *ledgerSorter) hold(job sortJob, until time.Time) {
	s.mu.Lock()
	s.held[owedSort{key: job.key, utteranceID: job.utteranceID}] = job
	s.mu.Unlock()
	s.wakeAt(until)
}

func (s *ledgerSorter) isHeld(job sortJob) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, held := s.held[owedSort{key: job.key, utteranceID: job.utteranceID}]
	return held
}

func (s *ledgerSorter) release(job sortJob) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.held, owedSort{key: job.key, utteranceID: job.utteranceID})
}

func (s *ledgerSorter) heldJobs() []sortJob {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.SortedFunc(maps.Values(s.held), func(a, b sortJob) int { return cmp.Compare(a.seq, b.seq) })
}

func (s *ledgerSorter) wakeAt(until time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wake.IsZero() || until.Before(s.wake) {
		s.wake = until
	}
}

func (s *ledgerSorter) takeWake() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := s.wake
	s.wake = time.Time{}
	return next
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

// Package prwatch links sessions to their pull requests and watches them:
// it reads each watched pull request from GitHub, wakes the session when
// something needs it, logs the outcome to the work the session holds, and
// settles the session's worktree once its branch merges.
package prwatch

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	prRateFloor      = 100
	BackoffFirst     = 4 * time.Minute
	prBackoffLongest = 30 * time.Minute
)

type Store interface {
	WatchedPRs() ([]registry.SessionPR, error)
	TaskForChild(childID string) (registry.Task, bool, error)
	QueueMessageWithKey(sessionID, message, key string) (int64, error)
	WorkForSession(sessionID string) (registry.WorkRecord, bool, error)
	SessionWork() (map[string]registry.WorkRecord, error)
	SessionHasPRIn(sessionID, repo string) (bool, error)
}

// Host is what the watcher needs from the daemon that runs it.
type Host interface {
	GitHub() github.Client
	// ChangePRs runs a registry write and republishes the session rows it changed.
	ChangePRs(write func(*registry.Registry) (bool, error)) (bool, error)
	// RowsChanged republishes session rows whose pull request problem changed.
	RowsChanged()
	Display(sessionID string) string
	// LogItem logs a line to one work item the session holds, marked as
	// written by the PR watcher, a source no request can set.
	LogItem(sessionID, itemID, line string)
	// OnlyActiveItem is the session's one work item in progress, or empty when
	// it has none or several.
	OnlyActiveItem(sessionID string) string
	SettleWork(sessionID, reason string)
	LatestTimeline(ctx context.Context, session *surface.Session, adapter surface.Surface, timeout time.Duration) ([]surface.TimelineItem, error)
	// TimelineText is an item's whole text, read from its stored body when the
	// timeline cut it short.
	TimelineText(sessionID string, item surface.TimelineItem) string
}

type Watcher struct {
	store Store
	host  Host
	log   *log.Logger
	state watchState
}

func New(store Store, host Host, logger *log.Logger) *Watcher {
	return &Watcher{store: store, host: host, log: logger}
}

type prHostState struct {
	problem string
	until   time.Time
	backoff time.Duration
}

type watchState struct {
	mu    sync.Mutex
	hosts map[string]*prHostState
}

func (s *watchState) host(name string) *prHostState {
	if s.hosts == nil {
		s.hosts = map[string]*prHostState{}
	}
	if s.hosts[name] == nil {
		s.hosts[name] = &prHostState{}
	}
	return s.hosts[name]
}

func (s *watchState) ready(host string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.host(host).until)
}

func (s *watchState) problem(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host(host).problem
}

func (s *watchState) record(host string, now time.Time, watch github.Watch, err error) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	state := s.host(host)
	before := state.problem
	var unavailable *github.UnavailableError
	var limited *github.RateLimitedError
	switch {
	case err == nil:
		state.problem, state.backoff, state.until = "", 0, time.Time{}
		if watch.RateRemaining < prRateFloor && watch.RateResetAt.After(now) {
			state.until = watch.RateResetAt
		}
	case errors.As(err, &unavailable):
		state.problem = unavailable.Reason
	case errors.As(err, &limited):
		state.backoff = min(max(2*state.backoff, BackoffFirst), prBackoffLongest)
		state.until = now.Add(state.backoff)
		state.problem = fmt.Sprintf("GitHub is rate limiting reads; the next read is at %s", state.until.Local().Format(time.Kitchen))
	}
	return state.problem != before
}

// Watch reads every watched pull request once, on each GitHub host that is not
// backing off.
func (w *Watcher) Watch(ctx context.Context, now time.Time) error {
	rows, err := w.store.WatchedPRs()
	if err != nil {
		return err
	}
	byHost := map[string][]registry.SessionPR{}
	for _, row := range rows {
		if repo, ok := github.ParseRepo(row.Repo); ok {
			byHost[repo.Host] = append(byHost[repo.Host], row)
		}
	}
	problemChanged := false
	var failures []error
	for host, hostRows := range byHost {
		if !w.state.ready(host, now) {
			continue
		}
		var refs []github.Ref
		seen := map[github.Ref]bool{}
		for _, row := range hostRows {
			if ref := prRef(row); !seen[ref] {
				seen[ref] = true
				refs = append(refs, ref)
			}
		}
		watch, err := w.host.GitHub().Watch(ctx, host, refs)
		problemChanged = w.state.record(host, now, watch, err) || problemChanged
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, row := range hostRows {
			status, found := watch.Statuses[prRef(row)]
			if !found {
				if _, err := w.host.ChangePRs(func(r *registry.Registry) (bool, error) {
					recorded, err := r.RecordPRReadFailure(row.SessionID, row.Repo, row.Number, row.Wakes)
					return recorded && row.ReadFailures+1 == registry.PRReadFailureLimit, err
				}); err != nil {
					return err
				}
				continue
			}
			verdict := decidePR(row, status, watch.Viewer)
			recorded := false
			if _, err := w.host.ChangePRs(func(r *registry.Registry) (bool, error) {
				var err error
				recorded, err = r.RecordPRRead(verdict.row, row.Wakes, now)
				return recorded && (prShownChange(row, verdict.row) || row.ReadFailures >= registry.PRReadFailureLimit), err
			}); err != nil {
				return err
			}
			if !recorded {
				continue
			}
			if len(verdict.reasons) > 0 {
				w.wake(verdict.row, verdict.reasons)
			}
			if line := outcomeLine(row, verdict.row); line != "" {
				w.host.LogItem(row.SessionID, row.ItemID, line)
			}
			if verdict.merged {
				w.settleMerged(row.SessionID, status.HeadRef)
			}
		}
	}
	if problemChanged {
		w.host.RowsChanged()
	}
	return errors.Join(failures...)
}

func prRef(row registry.SessionPR) github.Ref {
	repo, _ := github.ParseRepo(row.Repo)
	return github.Ref{Repo: repo, Number: row.Number}
}

func prShownChange(before, after registry.SessionPR) bool {
	return before.Title != after.Title || before.State != after.State || before.HeadSHA != after.HeadSHA || before.Mergeable != after.Mergeable ||
		before.Review != after.Review || before.Checks != after.Checks || before.Watch != after.Watch
}

func (w *Watcher) wake(pr registry.SessionPR, reasons []string) {
	target, lead := pr.SessionID, ""
	if task, found, err := w.store.TaskForChild(pr.SessionID); err == nil && found && task.State != registry.TaskRunning {
		target, lead = task.Parent, fmt.Sprintf("For %s, which already reported: ", w.host.Display(pr.SessionID))
	}
	message := prWakeMessage(lead, pr, reasons)
	key := fmt.Sprintf("pr:%s:%s#%d:%d", pr.SessionID, pr.Repo, pr.Number, pr.Wakes)
	if _, err := w.store.QueueMessageWithKey(target, message, key); err != nil {
		w.log.Printf("pull request wake for %s: %s", w.host.Display(target), err)
	}
}

func (w *Watcher) settleMerged(sessionID, headRef string) {
	record, found, err := w.store.WorkForSession(sessionID)
	if err != nil || !found || record.State != registry.WorkActive || record.Branch != headRef {
		return
	}
	w.host.SettleWork(sessionID, registry.WorkSettledMerged)
}

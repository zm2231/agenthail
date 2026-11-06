package daemon

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/github"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	prRateFloor      = 100
	prBackoffFirst   = 4 * time.Minute
	prBackoffLongest = 30 * time.Minute
)

type prHostState struct {
	problem string
	until   time.Time
	backoff time.Duration
}

type prWatchState struct {
	mu    sync.Mutex
	hosts map[string]*prHostState
}

func (s *prWatchState) host(name string) *prHostState {
	if s.hosts == nil {
		s.hosts = map[string]*prHostState{}
	}
	if s.hosts[name] == nil {
		s.hosts[name] = &prHostState{}
	}
	return s.hosts[name]
}

func (s *prWatchState) ready(host string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return !now.Before(s.host(host).until)
}

func (s *prWatchState) problem(host string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.host(host).problem
}

func (s *prWatchState) record(host string, now time.Time, watch github.Watch, err error) bool {
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
		state.backoff = min(max(2*state.backoff, prBackoffFirst), prBackoffLongest)
		state.until = now.Add(state.backoff)
		state.problem = fmt.Sprintf("GitHub is rate limiting reads; the next read is at %s", state.until.Local().Format(time.Kitchen))
	}
	return state.problem != before
}

func (d *Daemon) watchPRs(ctx context.Context, now time.Time) error {
	rows, err := d.Registry.WatchedPRs()
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
		if !d.prWatch.ready(host, now) {
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
		watch, err := d.github.Watch(ctx, host, refs)
		problemChanged = d.prWatch.record(host, now, watch, err) || problemChanged
		if err != nil {
			failures = append(failures, err)
			continue
		}
		for _, row := range hostRows {
			status, found := watch.Statuses[prRef(row)]
			if !found {
				if _, err := d.changePRs(func(r *registry.Registry) (bool, error) {
					recorded, err := r.RecordPRReadFailure(row.SessionID, row.Repo, row.Number, row.Wakes)
					return recorded && row.ReadFailures+1 == registry.PRReadFailureLimit, err
				}); err != nil {
					return err
				}
				continue
			}
			verdict := decidePR(row, status, watch.Viewer)
			recorded := false
			if _, err := d.changePRs(func(r *registry.Registry) (bool, error) {
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
				d.wakeForPR(verdict.row, verdict.reasons)
			}
			if verdict.merged {
				d.settleMergedPR(row.SessionID, status.HeadRef)
			}
		}
	}
	if problemChanged {
		d.requestCatalogDiscovery()
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

func (d *Daemon) wakeForPR(pr registry.SessionPR, reasons []string) {
	target, lead := pr.SessionID, ""
	if task, found, err := d.Registry.TaskForChild(pr.SessionID); err == nil && found && task.State != registry.TaskRunning {
		target, lead = task.Parent, fmt.Sprintf("For %s, which already reported: ", d.resolveDisplay(pr.SessionID))
	}
	message := prWakeMessage(lead, pr, reasons)
	key := fmt.Sprintf("pr:%s:%s#%d:%d", pr.SessionID, pr.Repo, pr.Number, pr.Wakes)
	if _, err := d.Registry.QueueMessageWithKey(target, message, key); err != nil {
		d.log.Printf("pull request wake for %s: %s", d.resolveDisplay(target), err)
	}
}

func (d *Daemon) settleMergedPR(sessionID, headRef string) {
	record, found, err := d.Registry.WorkForSession(sessionID)
	if err != nil || !found || record.State != registry.WorkActive || record.Branch != headRef {
		return
	}
	d.settleWork(sessionID, registry.WorkSettledMerged)
}

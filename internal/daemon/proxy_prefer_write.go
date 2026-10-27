package daemon

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/proxyacct"
)

type proxyStep struct {
	done string
	not  string
	run  func(context.Context) error
	mark *proxyPreferChange
}

// No rollback: undoing would mean more writes to a proxy that just failed one.
func (d *Daemon) applyProxyPrefer(ctx context.Context, config proxyacct.Config, entry *proxyUsageEntry, plan proxyPreferPlan) ([]string, error) {
	steps := []proxyStep{}
	for _, change := range plan.Changes {
		steps = append(steps, proxyStep{
			done: fmt.Sprintf("%s set to %g", change.Label, change.After),
			not:  change.Label + " not changed",
			run:  func(ctx context.Context) error { return entry.client.SetPreference(ctx, change.ID, change.After) },
			mark: &change,
		})
	}
	for _, account := range plan.Release {
		steps = append(steps, proxyStep{
			done: "sessions released from " + account.Label,
			not:  "sessions on " + account.Label + " not released",
			run:  func(ctx context.Context) error { return entry.client.ReleaseSessions(ctx, account.ID) },
		})
	}
	host := d.localHost().Name
	done := []string{}
	for i, step := range steps {
		if err := step.run(ctx); err != nil {
			parts := append(append([]string{}, done...), step.not+": "+proxyWriteProblem(config, err))
			for _, rest := range steps[i+1:] {
				parts = append(parts, rest.not)
			}
			return done, errors.New(strings.Join(parts, "; ") + ". Run it again to finish.")
		}
		if step.mark != nil {
			entry.mark(step.mark.ID, proxyMark{value: step.mark.After, host: host, at: time.Now()})
		}
		done = append(done, step.done)
	}
	return done, nil
}

func proxyWriteProblem(config proxyacct.Config, err error) string {
	switch proxyacct.ProblemOf(err) {
	case proxyacct.ReadOnly:
		return fmt.Sprintf("the key from %s can read the %s proxy but not change it", config.Management.Describe(), config.Name)
	case proxyacct.KeyRefused:
		return "the proxy refused the management key; Agenthail stops using it until the key changes"
	case proxyacct.Unreachable:
		return fmt.Sprintf("the proxy did not answer, so whether it applied is unknown (%v)", err)
	case proxyacct.NoAccount:
		return "the proxy no longer has this account"
	}
	return err.Error()
}

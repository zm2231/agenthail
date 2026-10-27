package daemon

import (
	"context"
	"fmt"
	"net/http"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) claudeRootPick(ctx context.Context) (claudepick.Pick, claudepick.Policy) {
	config, err := LoadDashboardConfig()
	policy := config.ClaudeAccountPolicy()
	if err != nil {
		policy = claudepick.DefaultPolicy()
	}
	now := time.Now()
	if roots := d.claudeConfigRoots(); len(roots) < 2 || !policy.Auto {
		candidates := make([]claudepick.Candidate, 0, len(roots))
		for _, root := range roots {
			candidate := claudepick.Candidate{Name: root.Name, Root: root.Root, Primary: root.Primary}
			if root.SignedInAs != nil {
				candidate.Account = root.SignedInAs.Email
			}
			candidates = append(candidates, candidate)
		}
		return claudepick.Choose(candidates, policy, now), policy
	}
	sets := d.claudeUsageSets(ctx, now)
	return claudepick.Choose(claudePickCandidates(sets, now), policy, now), policy
}

func claudePickCandidates(sets []usageSet, now time.Time) []claudepick.Candidate {
	candidates := make([]claudepick.Candidate, 0, len(sets))
	for _, set := range sets {
		candidate := claudepick.Candidate{Name: set.Name, Root: set.Root, Account: set.Account, Login: set.Login, Primary: set.Primary}
		for _, meter := range set.Meters {
			if !meter.Until.IsZero() && !now.Before(meter.Until) {
				continue
			}
			window := &claudepick.Window{Percent: meter.Percent, ResetsAt: meter.ResetsAt}
			switch meter.Kind {
			case "session", "5h":
				candidate.Session = window
			case "weekly", "weekly_all":
				candidate.Weekly = window
			}
		}
		candidates = append(candidates, candidate)
	}
	return candidates
}

func (d *Daemon) resolveClaudeStart(ctx context.Context, kind surface.SurfaceKind, options *surface.SessionStartOptions) *claudepick.Pick {
	if kind != surface.KindClaude || options.ConfigRoot != "" {
		return nil
	}
	pick, _ := d.claudeRootPick(ctx)
	options.ConfigRoot = pick.Root
	return &pick
}

func (d *Daemon) claudeRootNames() []string {
	names := []string{}
	for _, root := range d.claudeConfigRoots() {
		names = append(names, root.Name)
	}
	return names
}

func (d *Daemon) knownClaudeRoot(name string) error {
	if name == "" || slices.Contains(d.claudeRootNames(), name) {
		return nil
	}
	return fmt.Errorf("no configured Claude config root is named %q", name)
}

func (d *Daemon) claudeRootPickHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	pick, policy := d.claudeRootPick(r.Context())
	writeDashboardJSON(w, http.StatusOK, map[string]any{"pick": pick, "policy": policy})
}

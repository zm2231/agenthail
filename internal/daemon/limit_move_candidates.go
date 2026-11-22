package daemon

import (
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/claudepick"
	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) claudeCachedUsageSets(roots []surface.ClaudeConfigRoot, now time.Time) []usage.Set {
	reader, ok := d.adapters().ForSurface(surface.KindClaude).(claudeConfigRootReader)
	home, err := os.UserHomeDir()
	if !ok || err != nil {
		return nil
	}
	sets := []usage.Set{}
	for _, root := range roots {
		set := usage.Set{Provider: "claude", Name: root.Name, Root: root.Root, Primary: root.Primary}
		set.Login, set.LoginSource = surface.ClaudeRootLogin(home, root)
		if root.SignedInAs != nil {
			set.Account = root.SignedInAs.Email
		}
		if set.Login == surface.ClaudeLoginProxy {
			set.Proxy = &usage.Proxy{Name: root.Proxy}
		}
		if root.Error == "" && set.Login == surface.ClaudeLoginClaudeAI {
			set.Meters = usage.Meters("claude", d.claudeUsage.Cached(reader, root, now), now)
		}
		sets = append(sets, set)
	}
	return sets
}

func limitMoveCandidates(sets []usage.Set, now time.Time) []claudepick.Candidate {
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

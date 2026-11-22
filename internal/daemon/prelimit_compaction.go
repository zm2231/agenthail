package daemon

import (
	"cmp"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// A compact spends usage too, so none starts this near the limit, where it could be refused.
const preLimitCompactionCeiling = 98.0

type rootLimit struct {
	window   string
	percent  float64
	resetsAt time.Time
}

type preLimitCandidate struct {
	session *surface.Session
	look    idleLook
}

type preLimitPass struct {
	d          *Daemon
	busy       map[string]bool
	unread     bool
	limits     func(root string) (rootLimit, bool)
	candidates map[string][]preLimitCandidate
}

func (d *Daemon) newPreLimitPass(sessions []surface.Session, now time.Time) *preLimitPass {
	pass := &preLimitPass{d: d, busy: map[string]bool{}, limits: d.rootLimits(now), candidates: map[string][]preLimitCandidate{}}
	inFlight, err := d.Registry.IdleCompactionsInFlight()
	if err != nil {
		d.log.Printf("read idle compactions in flight: %s", err)
		pass.unread = true
		return pass
	}
	for _, session := range sessions {
		if inFlight[session.ID] {
			pass.busy[claudeTranscriptRoot(session.Transcript)] = true
		}
	}
	return pass
}

func (p *preLimitPass) nearLimit(root string, settings surface.IdleCompaction) (rootLimit, bool) {
	if settings.PreLimitPercent == 0 {
		return rootLimit{}, false
	}
	limit, ok := p.limits(root)
	return limit, ok && limit.percent >= float64(settings.PreLimitPercent)
}

// Near a limit every compact spends scarce usage, so a root's sessions take turns.
func (p *preLimitPass) hold(root string, settings surface.IdleCompaction) string {
	if _, near := p.nearLimit(root, settings); near && p.busy[root] {
		return "another session under this root is compacting near its usage limit"
	}
	return ""
}

func (p *preLimitPass) queued(root string) {
	p.busy[root] = true
}

func (p *preLimitPass) add(root string, candidate preLimitCandidate) {
	p.candidates[root] = append(p.candidates[root], candidate)
}

func (p *preLimitPass) run() {
	if p.unread {
		return
	}
	for root, list := range p.candidates {
		limit, near := p.nearLimit(root, list[0].look.settings)
		if p.busy[root] || !near || limit.percent >= preLimitCompactionCeiling {
			continue
		}
		slices.SortFunc(list, func(a, b preLimitCandidate) int {
			return cmp.Compare(b.look.state.ContextTokens, a.look.state.ContextTokens)
		})
		for _, candidate := range list {
			if p.d.queuePreLimitCompaction(root, limit, candidate) {
				p.queued(root)
				break
			}
		}
	}
}

func (d *Daemon) queuePreLimitCompaction(root string, limit rootLimit, candidate preLimitCandidate) bool {
	state := candidate.look.state
	expires := state.LastRequestAt.Add(idleCompactionBefore)
	if !limit.resetsAt.IsZero() && !limit.resetsAt.After(expires) {
		return false
	}
	outcome, err := d.Registry.QueueIdleCompaction(candidate.session.ID, state.LastRequestAt)
	if err != nil {
		d.log.Printf("queue pre-limit compaction for %s: %s", d.resolveDisplay(candidate.session.ID), err)
		return false
	}
	if outcome != registry.IdleCompactionQueued {
		return false
	}
	resets := "reset unknown"
	if !limit.resetsAt.IsZero() {
		resets = "resets " + limit.resetsAt.Local().Format("15:04")
	}
	d.log.Printf("queued pre-limit compaction for %s at %d tokens: %s %s at %.0f%%, %s, cache expires %s", d.resolveDisplay(candidate.session.ID), state.ContextTokens, root, limit.window, limit.percent, resets, expires.Local().Format("15:04"))
	return true
}

// A proxy root reads the proxy's own fill, since the proxy, not the root, picks the account.
func (d *Daemon) rootLimits(now time.Time) func(root string) (rootLimit, bool) {
	var roots []surface.ClaudeConfigRoot
	var view *proxyView
	read := map[string]rootLimit{}
	known := map[string]bool{}
	return func(path string) (rootLimit, bool) {
		if limit, seen := read[path]; seen {
			return limit, known[path]
		}
		if roots == nil {
			roots = d.currentClaudeConfigRoots()
		}
		var limit rootLimit
		ok := false
		root, found := surface.ClaudeConfigRootFor(roots, path)
		switch {
		case found && root.Proxy != "":
			if view == nil {
				readView := d.readProxyView(now)
				view = &readView
			}
			if reading := view.readings[root.Proxy]; reading != nil {
				limit.window = "proxy " + root.Proxy
				limit.percent, limit.resetsAt, ok = proxyacct.Fill(reading.Accounts, now)
			}
		case found:
			for _, set := range d.claudeCachedUsageSets([]surface.ClaudeConfigRoot{root}, now) {
				limit, ok = bindingWindow(set.Meters, now)
			}
		}
		read[path], known[path] = limit, ok
		return limit, ok
	}
}

func bindingWindow(meters []usage.Meter, now time.Time) (rootLimit, bool) {
	var limit rootLimit
	ok := false
	for _, meter := range meters {
		if !meter.Until.IsZero() && !now.Before(meter.Until) {
			continue
		}
		window := map[string]string{"session": "5h", "5h": "5h", "weekly": "weekly", "weekly_all": "weekly"}[meter.Kind]
		if window == "" || (ok && meter.Percent <= limit.percent) {
			continue
		}
		limit, ok = rootLimit{window: window, percent: meter.Percent, resetsAt: meter.ResetsAt}, true
	}
	return limit, ok
}

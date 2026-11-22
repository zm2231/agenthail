package daemon

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon/usage"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/proxyacct"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// limitMoveLead is how far a stop's own reset must lie ahead for a move to
// another root to be worth more than waiting for it.
const limitMoveLead = 15 * time.Minute

// moveAfterLimitEnabled reads the host's setting; it is on unless turned off.
func (d *Daemon) moveAfterLimitEnabled() bool {
	config, err := dashconfig.Load()
	if err != nil {
		d.log.Printf("read move after limit setting: %s", err)
		return true
	}
	return config.MovesAfterLimit()
}

// proxyView is the host's Claude config roots and the cached accounts of
// each readable proxy, read once for every stop a pass looks at. It writes
// nothing to any proxy.
type proxyView struct {
	roots    []surface.ClaudeConfigRoot
	readings map[string]*usage.ProxyReading
}

func (d *Daemon) readProxyView(now time.Time) proxyView {
	view := proxyView{roots: d.currentClaudeConfigRoots(), readings: map[string]*usage.ProxyReading{}}
	home, err := os.UserHomeDir()
	if err != nil {
		return view
	}
	configs, err := surface.ConfiguredProxies(home)
	if err != nil {
		return view
	}
	for _, config := range configs {
		if reading := d.proxyUsage.Cached(config, now); reading != nil && reading.Err == nil {
			view.readings[config.Name] = reading
		}
	}
	return view
}

// room reports whether the named proxy would route new work for a session
// now, by the proxy's own selection: some Claude account it would pick is
// live and under every ceiling that binds the session's model. An unread or
// unreadable proxy has no room.
func (v proxyView) room(name string, fable bool, now time.Time) bool {
	reading := v.readings[name]
	if reading == nil {
		return false
	}
	if _, full := proxyacct.Full(reading.Accounts, now, fable); full {
		return false
	}
	_, ok := proxyacct.Pick(reading.Accounts, "claude", now, fable)
	return ok
}

// freesAt is when the named proxy, full now, next has an account with room
// for a session of that model; zero when it has room, is unread, or cannot
// say.
func (v proxyView) freesAt(name string, fable bool, now time.Time) time.Time {
	reading := v.readings[name]
	if reading == nil {
		return time.Time{}
	}
	if frees, full := proxyacct.Full(reading.Accounts, now, fable); full && frees.After(now) {
		return frees
	}
	return time.Time{}
}

// proxyRootsBut is the usable roots that run through a proxy, in order, other
// than from and any root through from's own proxy, which reach the same
// accounts.
func (v proxyView) proxyRootsBut(from string) []surface.ClaudeConfigRoot {
	own, _ := surface.ClaudeConfigRootFor(v.roots, from)
	var out []surface.ClaudeConfigRoot
	for _, root := range v.roots {
		if root.Proxy == "" || root.Error != "" || root.Missing || surface.SameClaudeRoot(root.Root, from) || own.Proxy != "" && root.Proxy == own.Proxy {
			continue
		}
		out = append(out, root)
	}
	return out
}

// rootWithRoom is the first of proxyRootsBut whose proxy has room for a
// session of that model.
func (v proxyView) rootWithRoom(from string, fable bool, now time.Time) (surface.ClaudeConfigRoot, bool) {
	for _, root := range v.proxyRootsBut(from) {
		if v.room(root.Proxy, fable, now) {
			return root, true
		}
	}
	return surface.ClaudeConfigRoot{}, false
}

func fableModel(model string) bool {
	return strings.Contains(strings.ToLower(model), "fable")
}

// limitSwitchPlan is what limitSwitch may do for a stop: restart it on its
// own root, at once when settled, else when ownProxy has room; or move it to
// the first of moveTo whose proxy has room.
type limitSwitchPlan struct {
	fable    bool
	settled  bool
	ownProxy string
	moveTo   []surface.ClaudeConfigRoot
}

// planLimitSwitch is the switch open to a waiting stop that has not been
// acted on, before its own reset: a restart when its own root has room (a
// proxy root counts the proxy's room, a direct root a turn that ended there
// without an error), else a move to a proxy root with room. A restart on a
// turn that ended is open to any session, so one the host does not run gets
// its note; the proxy's room and a move are only for a session the Claude
// host runs. A move is never for a coordinator, never to a directly
// signed-in root, and not when the stop's reset is close.
func (d *Daemon) planLimitSwitch(view proxyView, stop registry.LimitResume, moveEnabled bool, now time.Time) (limitSwitchPlan, bool) {
	session, err := d.Registry.Session(stop.SessionID)
	if err != nil {
		return limitSwitchPlan{}, false
	}
	hosted := session.RuntimeKind == surface.RuntimeClaudeHost
	plan := limitSwitchPlan{fable: fableModel(session.ConfiguredModel)}
	own, _ := surface.ClaudeConfigRootFor(view.roots, stop.AccountKey)
	if hosted && own.Proxy != "" {
		plan.ownProxy = own.Proxy
		return plan, true
	}
	if d.limitResumes.settledAfter(stop.AccountKey, stop.RefusedAt) {
		plan.settled = true
		return plan, true
	}
	if !hosted || !moveEnabled || !stop.ResetsAt.IsZero() && stop.ResetsAt.Sub(now) < limitMoveLead {
		return limitSwitchPlan{}, false
	}
	if _, lead, err := d.Registry.CoordinatorOfSession(stop.SessionID); err != nil || lead {
		return limitSwitchPlan{}, false
	}
	plan.moveTo = view.proxyRootsBut(stop.AccountKey)
	return plan, len(plan.moveTo) > 0
}

func (d *Daemon) limitSwitch(view proxyView, stop registry.LimitResume, moveEnabled bool, now time.Time) (limitRelease, bool) {
	plan, ok := d.planLimitSwitch(view, stop, moveEnabled, now)
	switch {
	case !ok:
		return limitRelease{}, false
	case plan.settled:
		return limitRelease{stop: stop, restart: true}, true
	case plan.ownProxy != "":
		return limitRelease{stop: stop, restart: true}, view.room(plan.ownProxy, plan.fable, now)
	}
	for _, root := range plan.moveTo {
		if view.room(root.Proxy, plan.fable, now) {
			return limitRelease{stop: stop, moveTo: root.Name, moveVia: root.Proxy}, true
		}
	}
	return limitRelease{}, false
}

// limitResumesAt is when a waiting stop is next acted on: its own reset, or
// sooner, when a full proxy that limitSwitch would act through frees an
// account first. A move counts only while the stop's reset is then still far.
// A stop with no reset is probed on its own schedule, so it shows none.
func (d *Daemon) limitResumesAt(view proxyView, stop registry.LimitResume, enabled, moveEnabled bool, now time.Time) time.Time {
	at := stop.ResetsAt
	if at.IsZero() || !enabled || stop.State != registry.LimitResumeWaiting || stop.Attempts > 0 {
		return at
	}
	plan, ok := d.planLimitSwitch(view, stop, moveEnabled, now)
	if !ok || plan.settled {
		return at
	}
	if plan.ownProxy != "" {
		if frees := view.freesAt(plan.ownProxy, plan.fable, now); !frees.IsZero() && frees.Before(at) {
			at = frees
		}
		return at
	}
	by := stop.ResetsAt.Add(-limitMoveLead)
	for _, root := range plan.moveTo {
		if frees := view.freesAt(root.Proxy, plan.fable, now); !frees.IsZero() && frees.Before(by) && frees.Before(at) {
			at = frees
		}
	}
	return at
}

func (d *Daemon) sessionIsFable(sessionID string) bool {
	session, err := d.Registry.Session(sessionID)
	return err == nil && fableModel(session.ConfiguredModel)
}

// moveLimitedSession moves a waiting session to a proxy root with room, in
// the background so a slow move holds up no other stop; the move resumes it
// there. A move that fails counts as the stop's attempt, so the stop then
// waits for its own reset. It reports whether it acted.
func (d *Daemon) moveLimitedSession(release limitRelease, now time.Time) bool {
	stop, root := release.stop, release.moveTo
	claimed, _ := d.claimLimitMove(stop.SessionID, stop.TurnID)
	if !claimed {
		return false
	}
	d.log.Printf("move %s from %s to %s: its usage limit stopped it and the %s proxy has room", d.resolveDisplay(stop.SessionID), stop.AccountKey, root, release.moveVia)
	started := d.background.start(func(ctx context.Context) {
		defer d.unclaimLimitMove(stop.SessionID)
		if _, err := d.moveSessionToRoot(ctx, sessionMoveRequest{SessionID: stop.SessionID, Root: root}); err != nil {
			d.log.Printf("move %s after its usage limit: %s", d.resolveDisplay(stop.SessionID), err)
			if _, err := d.Registry.CountLimitAttempt(stop.SessionID, stop.TurnID, now); err != nil {
				d.log.Printf("count the move of %s: %s", d.resolveDisplay(stop.SessionID), err)
			}
			d.limitResumeChanged(stop.SessionID)
		}
	})
	if !started {
		d.unclaimLimitMove(stop.SessionID)
	}
	return started
}

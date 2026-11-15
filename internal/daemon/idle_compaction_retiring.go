package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const retiringListTimeout = 2 * time.Second

// leavingHost is a Claude host that closes a session once the daemon
// releases it: a retiring host for each of its sessions, or the current host
// for a session it reopens to apply a new compact window.
type leavingHost struct {
	claudehost.Client
	retiring bool
	leaving  int
}

func (h leavingHost) where() string {
	if h.retiring {
		return "on the retiring Claude host"
	}
	return "open before its new compact window applies"
}

// leavingClaudeSessions is each session a Claude host waits to close until
// the daemon releases it, by the host that runs it.
func (d *Daemon) leavingClaudeSessions(ctx context.Context) map[string]leavingHost {
	if d.claudeHost == nil {
		return nil
	}
	current := d.claudeHost.Client()
	out := map[string]leavingHost{}
	for _, host := range append([]leavingHost{{Client: current}}, retiringHosts(current)...) {
		listCtx, cancel := context.WithTimeout(ctx, retiringListTimeout)
		listing, err := host.Own(listCtx)
		cancel()
		if err != nil {
			continue
		}
		for _, session := range listing.Sessions {
			if host.retiring || session.PendingAutoCompactWindow != nil {
				leaving := host
				leaving.leaving = session.Leaving
				out[session.SessionID] = leaving
			}
		}
	}
	return out
}

func retiringHosts(current claudehost.Client) []leavingHost {
	var out []leavingHost
	for _, host := range current.Retiring() {
		out = append(out, leavingHost{Client: host, retiring: true})
	}
	return out
}

// retiringDecision is what the daemon last decided for a session a retiring
// host runs, so each decision is logged once per request.
type retiringDecision struct {
	host      string
	leaving   int
	requestAt time.Time
	keep      bool
	released  bool
}

type retiringDecisions struct {
	mu      sync.Mutex
	decided map[string]retiringDecision
}

// change records the decision and reports whether it differs from the last
// one for the session on that host and request.
func (r *retiringDecisions) change(sessionID string, next retiringDecision) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.decided == nil {
		r.decided = map[string]retiringDecision{}
	}
	last, ok := r.decided[sessionID]
	if ok && last.host == next.host && last.leaving == next.leaving && last.requestAt.Equal(next.requestAt) && last.keep == next.keep {
		return false
	}
	r.decided[sessionID] = next
	return true
}

func (r *retiringDecisions) markReleased(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if decision, ok := r.decided[sessionID]; ok {
		decision.released = true
		r.decided[sessionID] = decision
	}
}

// New work or a new compact window starts a new leaving generation, which the
// host keeps until the daemon releases it again.
func (r *retiringDecisions) released(sessionID, host string, leaving int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	decision, ok := r.decided[sessionID]
	return ok && decision.host == host && decision.leaving == leaving && decision.released
}

func (r *retiringDecisions) forgetExcept(retiring map[string]leavingHost) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.decided {
		if _, ok := retiring[id]; !ok {
			delete(r.decided, id)
		}
	}
}

// decideRetiring keeps a session open on the host that waits to close it
// while its idle compaction could still run there, until it has compacted or
// its prompt cache has expired, and releases it otherwise so the host closes
// it at its next idle point. A released session stays released.
func (d *Daemon) decideRetiring(ctx context.Context, host leavingHost, session *surface.Session, look idleLook, refused bool) {
	if d.retiringDecisions.released(session.ID, host.Socket, host.leaving) {
		return
	}
	keep, why := retiringKeep(session, look, refused)
	decision := retiringDecision{host: host.Socket, leaving: host.leaving, requestAt: look.state.LastRequestAt, keep: keep}
	if d.retiringDecisions.change(session.ID, decision) {
		if keep {
			due := look.state.LastRequestAt.Add(idleCompactionAfter)
			d.log.Printf("keeping %s %s for its idle compaction at %d tokens, due %s", d.resolveDisplay(session.ID), host.where(), look.state.ContextTokens, due.Local().Format("15:04"))
		} else {
			d.log.Printf("releasing %s %s: %s", d.resolveDisplay(session.ID), host.where(), why)
		}
	}
	if keep {
		return
	}
	releaseCtx, cancel := context.WithTimeout(ctx, retiringListTimeout)
	err := host.Release(releaseCtx, session.ID, host.leaving)
	cancel()
	// A host that predates release answers invalid; it closes idle sessions
	// at once without being asked. not_leaving is a compact window chosen
	// back to the one in effect, so the host keeps the session open anyway.
	if err == nil || claudehost.IsCode(err, "not_open") || claudehost.IsCode(err, "invalid") || claudehost.IsCode(err, "not_leaving") {
		d.retiringDecisions.markReleased(session.ID)
		return
	}
	d.log.Printf("release %s %s: %s", d.resolveDisplay(session.ID), host.where(), err)
}

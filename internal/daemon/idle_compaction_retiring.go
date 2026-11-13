package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

const retiringListTimeout = 2 * time.Second

// retiringClaudeSessions is each session a retiring Claude host runs, by the
// host that runs it.
func (d *Daemon) retiringClaudeSessions(ctx context.Context) map[string]claudehost.Client {
	if d.claudeHost == nil {
		return nil
	}
	out := map[string]claudehost.Client{}
	for _, host := range d.claudeHost.Client().Retiring() {
		listCtx, cancel := context.WithTimeout(ctx, retiringListTimeout)
		listing, err := host.Own(listCtx)
		cancel()
		if err != nil {
			continue
		}
		for _, session := range listing.Sessions {
			out[session.SessionID] = host
		}
	}
	return out
}

// retiringDecision is what the daemon last decided for a session a retiring
// host runs, so each decision is logged once per request.
type retiringDecision struct {
	host      string
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
	if ok && last.host == next.host && last.requestAt.Equal(next.requestAt) && last.keep == next.keep {
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

func (r *retiringDecisions) released(sessionID, host string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	decision, ok := r.decided[sessionID]
	return ok && decision.host == host && decision.released
}

func (r *retiringDecisions) forgetExcept(retiring map[string]claudehost.Client) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id := range r.decided {
		if _, ok := retiring[id]; !ok {
			delete(r.decided, id)
		}
	}
}

// decideRetiring keeps a session on the retiring host that runs it while its
// idle compaction could still run there, until it has compacted or its prompt
// cache has expired, and releases it otherwise so the host closes it at its
// next idle point. A released session stays released.
func (d *Daemon) decideRetiring(ctx context.Context, host claudehost.Client, session *surface.Session, look idleLook, refused bool) {
	if d.retiringDecisions.released(session.ID, host.Socket) {
		return
	}
	keep, why := retiringKeep(session, look, refused)
	decision := retiringDecision{host: host.Socket, requestAt: look.state.LastRequestAt, keep: keep}
	if d.retiringDecisions.change(session.ID, decision) {
		if keep {
			due := look.state.LastRequestAt.Add(idleCompactionAfter)
			d.log.Printf("keeping %s on the retiring Claude host for its idle compaction at %d tokens, due %s", d.resolveDisplay(session.ID), look.state.ContextTokens, due.Local().Format("15:04"))
		} else {
			d.log.Printf("releasing %s from the retiring Claude host: %s", d.resolveDisplay(session.ID), why)
		}
	}
	if keep {
		return
	}
	releaseCtx, cancel := context.WithTimeout(ctx, retiringListTimeout)
	err := host.Release(releaseCtx, session.ID)
	cancel()
	// A host that predates release answers invalid; it closes idle sessions
	// at once without being asked.
	if err == nil || claudehost.IsCode(err, "not_open") || claudehost.IsCode(err, "invalid") {
		d.retiringDecisions.markReleased(session.ID)
		return
	}
	d.log.Printf("release %s from the retiring Claude host: %s", d.resolveDisplay(session.ID), err)
}

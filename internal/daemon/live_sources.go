package daemon

import "github.com/zm2231/agenthail/internal/surface"

// liveSessionHolder holds the source of a session whose process is live, so
// its journal follows the transcript while nothing else watches it.
const liveSessionHolder = "live"

// liveSession reports whether a listed session runs now: its adapter found its
// process open, or it is a managed Codex thread in a turn. A Codex Desktop
// thread has no process of its own to check, so it is read when asked for.
func liveSession(session surface.Session, open bool) bool {
	return open || session.RuntimeKind == surface.RuntimeCodexAppServer && session.Status == surface.StatusBusy
}

// followLiveSessions holds a source for each of a surface's live sessions and
// releases the ones its last pass no longer found live. A partial pass only
// adds, since a session it did not list may still run.
func (d *Daemon) followLiveSessions(kind surface.SurfaceKind, sessions []surface.Session, open map[string]bool, complete bool) {
	live := map[string]surface.Session{}
	for _, session := range sessions {
		if liveSession(session, open[session.ID]) {
			live[session.ID] = session
		}
	}
	d.sourceHoldMu.Lock()
	if d.liveSources == nil {
		d.liveSources = map[string]surface.SurfaceKind{}
	}
	var released []string
	for id, held := range d.liveSources {
		if _, found := live[id]; held == kind && !found && complete {
			released = append(released, id)
			delete(d.liveSources, id)
		}
	}
	for id := range live {
		d.liveSources[id] = kind
	}
	d.sourceHoldMu.Unlock()
	for _, id := range released {
		d.releaseSessionSource(id, liveSessionHolder)
	}
	for id := range live {
		d.sourceHoldMu.Lock()
		_, held := d.sourceHolds[id][liveSessionHolder]
		d.sourceHoldMu.Unlock()
		if held {
			continue
		}
		stored, err := d.Registry.Session(id)
		if err != nil {
			d.log.Printf("follow live session %s: %s", d.resolveDisplay(id), err)
			continue
		}
		d.setSessionSourceHold(stored, true, liveSessionHolder)
	}
}

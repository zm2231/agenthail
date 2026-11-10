package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// lockWaiting serializes, per session, every read of what a turn waits on
// with the writes that follow from it: the waiting record and the catalog row
// that shows it. Observation and catalog discovery run concurrently, so
// without it an older read could bring back a question the user answered.
func (d *Daemon) lockWaiting(sessionID string) func() {
	value, _ := d.waitingLocks.LoadOrStore(sessionID, &sync.Mutex{})
	lock := value.(*sync.Mutex)
	if !lock.TryLock() {
		if d.waitingContended != nil {
			d.waitingContended(sessionID)
		}
		lock.Lock()
	}
	return lock.Unlock
}

// observeTurn reads the session's turn and records what it is waiting on as
// one step. A changed record is published to the session's catalog row
// before it returns, so a client reading right after sees the change. The
// waiting lock is released first: the publish takes it again, as every
// catalog row write does.
func (d *Daemon) observeTurn(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, error) {
	unlock := d.lockWaiting(session.ID)
	observation, changed, err := d.observeTurnLocked(ctx, adapter, session)
	unlock()
	if changed {
		d.publishWaiting(ctx, session.ID, observation.WaitingOn)
		if observation.WaitingOn != nil {
			d.publishTaskRequest(session.ID)
		}
	}
	return observation, err
}

func (d *Daemon) publishWaiting(ctx context.Context, sessionID string, waiting *surface.Waiting) {
	d.publishLive(ctx, sessionID, func(session *surface.Session) {
		session.WaitingOn = waiting
		if waiting != nil {
			// A turn waiting on the person is not quiet; the next status pass
			// reads its activity again once the wait ends.
			session.Status = surface.StatusBusy
			session.Activity = nil
		}
	})
}

// publishRoute writes a session's catalog row with the route a send read
// from its process, so its notice describes the route the send takes.
func (d *Daemon) publishRoute(reached surface.Session) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	d.publishLive(ctx, reached.ID, func(session *surface.Session) {
		session.Bridge, session.Transport, session.PID = reached.Bridge, reached.Transport, reached.PID
	})
}

// publishLive writes a session's catalog row with change applied, through
// the entry the last discovery pass left for status refreshes. A session
// without one, or whose row did not commit, waits for a discovery pass
// instead.
func (d *Daemon) publishLive(ctx context.Context, sessionID string, change func(*surface.Session)) {
	d.catalogLiveMu.Lock()
	live := d.catalogLive[sessionID]
	var session surface.Session
	if live != nil {
		session = live.session
	}
	d.catalogLiveMu.Unlock()
	if live == nil {
		d.requestCatalogDiscovery()
		return
	}
	change(&session)
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("catalog config: %s", err)
		d.requestCatalogDiscovery()
		return
	}
	counts, err := d.Registry.QueueCounts()
	if err != nil {
		d.log.Printf("catalog queue counts: %s", err)
		d.requestCatalogDiscovery()
		return
	}
	view, err := d.loadCatalogViewState()
	if err != nil {
		d.log.Printf("catalog view state: %s", err)
		d.requestCatalogDiscovery()
		return
	}
	defer d.publishUnarchivedProjects()
	current := func() bool {
		d.catalogLiveMu.Lock()
		defer d.catalogLiveMu.Unlock()
		return d.catalogLive[sessionID] == live
	}
	d.publishCatalogSession(ctx, live.adapter, session, live.identity, live.alias, counts[sessionID], d.limitResumeRow(sessionID), live.open, config, view, time.Now().UTC(), current, func(committed bool) {
		d.catalogLiveMu.Lock()
		defer d.catalogLiveMu.Unlock()
		if !committed {
			d.requestCatalogDiscovery()
			return
		}
		if d.catalogLive[sessionID] == live {
			live.session = session
		}
	})
}

func (d *Daemon) observeTurnLocked(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, bool, error) {
	observation, err := adapter.Observe(ctx, session)
	if err != nil || observation == nil {
		return observation, false, err
	}
	if observation.WaitingUnknown {
		observation.WaitingOn = d.recordedWaiting(*session)
		return observation, false, nil
	}
	waiting := observation.WaitingOn
	if observation.Status != surface.StatusBusy {
		waiting = nil
	}
	changed := d.syncWaits(*session, waiting)
	observation.WaitingOn = d.recordedWaiting(*session)
	return observation, changed, nil
}

// catalogWaiting returns what a listed session is waiting on. A listing that
// agrees with the open asks is taken as it is; one that disagrees is
// confirmed by observing the turn, since a listing may be older than the
// asks. The caller holds the session's waiting lock until its catalog row
// commits.
func (d *Daemon) catalogWaiting(ctx context.Context, adapter surface.Surface, session surface.Session) *surface.Waiting {
	listed := session.WaitingOn
	if session.Status != surface.StatusBusy {
		listed = nil
	}
	open, err := d.Registry.OpenPermissionAsks(session.ID)
	if err != nil {
		d.log.Printf("read waiting %s: %s", d.resolveDisplay(session.ID), err)
		return nil
	}
	if waitsAgree(open, listed) {
		d.tellPermissionAsks(open)
		return d.recordedWaiting(session)
	}
	observeCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	observation, _, err := d.observeTurnLocked(observeCtx, adapter, &session)
	if err != nil || observation == nil {
		return d.recordedWaiting(session)
	}
	return observation.WaitingOn
}

func (d *Daemon) restartable(session surface.Session) bool {
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return false
	}
	lifecycle := surface.EffectiveCapabilities(adapter, &session).Lifecycle
	return lifecycle.Stop && lifecycle.Resume
}

// elsewhereWaitingMessage says what a session waits on when Agenthail
// cannot read the prompt itself. A terminal session's prompt that Agenthail
// does not hold is answered in its terminal; a hosted or background one has
// nowhere to answer it, so stopping and resuming the session clears it.
func elsewhereWaitingMessage(name string, waiting surface.Waiting, runtime surface.RuntimeKind, restartable bool) string {
	asks := map[surface.WaitingKind]string{
		surface.WaitingQuestion: "asks you a question",
		surface.WaitingInput:    "needs your input",
		surface.WaitingDialog:   "has a dialog open in its terminal",
	}[waiting.Kind]
	if asks == "" {
		asks = "needs your approval"
	}
	if !waiting.Answerable && waiting.Kind != surface.WaitingDialog {
		switch {
		case restartable:
			return fmt.Sprintf("%s is waiting on a prompt Agenthail can't reach. Stop and resume it to clear it", name)
		case runtime == surface.RuntimeClaudeCLI:
			return fmt.Sprintf("%s %s in its terminal", name, asks)
		default:
			return fmt.Sprintf("%s %s. Agenthail can't answer it; answer it where it runs", name, asks)
		}
	}
	return fmt.Sprintf("%s %s", name, asks)
}

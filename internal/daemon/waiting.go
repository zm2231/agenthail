package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// observeTurn reads the session's turn and records what it is waiting on as
// one step per session. Observation and catalog discovery run concurrently,
// so a read and its write are serialized: the last read of the provider is
// always the last write, and an older read cannot bring back a question the
// user has answered.
func (d *Daemon) observeTurn(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, error) {
	lock, _ := d.waitingLocks.LoadOrStore(session.ID, &sync.Mutex{})
	lock.(*sync.Mutex).Lock()
	defer lock.(*sync.Mutex).Unlock()
	observation, err := adapter.Observe(ctx, session)
	if err != nil || observation == nil {
		return observation, err
	}
	waiting := observation.WaitingOn
	if observation.Status != surface.StatusBusy {
		waiting = nil
	}
	recorded, announce, err := d.Registry.RecordWaiting(session.ID, waiting, time.Now())
	if err != nil {
		d.log.Printf("record waiting %s: %s", d.resolveDisplay(session.ID), err)
		return observation, nil
	}
	if announce {
		d.announceWaiting(*session, *recorded)
	}
	observation.WaitingOn = recorded
	return observation, nil
}

// catalogWaiting returns what a listed session is waiting on. A listing that
// agrees with the record is taken as it is; one that disagrees is confirmed
// by observing the turn, since a listing may be older than the record.
func (d *Daemon) catalogWaiting(ctx context.Context, adapter surface.Surface, session surface.Session) *surface.Waiting {
	listed := session.WaitingOn
	if session.Status != surface.StatusBusy {
		listed = nil
	}
	recorded, announced, err := d.Registry.WaitingState(session.ID)
	if err != nil {
		d.log.Printf("read waiting %s: %s", d.resolveDisplay(session.ID), err)
		return listed
	}
	if surface.SameWaiting(recorded, listed) {
		if recorded != nil && !announced {
			d.announceWaiting(session, *recorded)
		}
		return recorded
	}
	observeCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	observation, err := d.observeTurn(observeCtx, adapter, &session)
	if err != nil || observation == nil {
		return recorded
	}
	return observation.WaitingOn
}

// announceWaiting tells the user, on the desktop and on paired devices, that
// a session is waiting on them. The record is marked announced only after
// both were attempted, so a daemon stopped in between announces the question
// again after it restarts rather than never.
func (d *Daemon) announceWaiting(session surface.Session, waiting surface.Waiting) {
	d.announceMu.Lock()
	if d.announcing == nil {
		d.announcing = map[string]string{}
	}
	if d.announcing[session.ID] == waiting.ID {
		d.announceMu.Unlock()
		return
	}
	d.announcing[session.ID] = waiting.ID
	d.announceMu.Unlock()
	desktopMessage := fmt.Sprintf("%s is waiting on you", d.resolveDisplay(session.ID))
	mobileMessage := fmt.Sprintf("%s is waiting on you", notificationSurfaceName(session.Surface))
	go func(sessionID, questionID string) {
		defer func() {
			d.announceMu.Lock()
			if d.announcing[sessionID] == questionID {
				delete(d.announcing, sessionID)
			}
			d.announceMu.Unlock()
		}()
		if err := Notify("Agenthail", desktopMessage, sessionID); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, "Agenthail", mobileMessage, sessionID, questionID, "session.waiting")
		if err := d.Registry.MarkWaitingAnnounced(sessionID, questionID); err != nil {
			d.log.Printf("mark waiting announced %s: %s", sessionID, err)
		}
	}(session.ID, waiting.ID)
}

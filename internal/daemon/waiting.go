package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// noteWaiting records the question a session is waiting on and notifies the
// user the first time each question is seen. It returns the recorded waiting
// state, which carries when the question was first seen. Observation and
// catalog discovery both call it, so a question is announced once whichever
// sees it first, including after a daemon restart. An observation reads the
// provider's turn directly and clears a question it no longer shows. A
// catalog source may list a busy session without reading its turn, so it
// clears a question only when it shows the session idle or offline.
func (d *Daemon) noteWaiting(session surface.Session, observed bool) *surface.Waiting {
	settled := session.Status == surface.StatusIdle || session.Status == surface.StatusOffline
	if session.Status != surface.StatusBusy {
		session.WaitingOn = nil
	}
	if session.WaitingOn == nil && !observed && !settled {
		return nil
	}
	recorded, isNew, err := d.Registry.RecordWaiting(session.ID, session.WaitingOn, time.Now())
	if err != nil {
		d.log.Printf("record waiting %s: %s", d.resolveDisplay(session.ID), err)
		return session.WaitingOn
	}
	if isNew && recorded != nil {
		desktopMessage := fmt.Sprintf("%s is waiting on you", d.resolveDisplay(session.ID))
		mobileMessage := fmt.Sprintf("%s is waiting on you", notificationSurfaceName(session.Surface))
		go func(sessionID, questionID string) {
			if err := Notify("Agenthail", desktopMessage, sessionID); err != nil {
				d.log.Printf("desktop notification: %s", err)
			}
			notificationCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			d.notifyPairedDevices(notificationCtx, "Agenthail", mobileMessage, sessionID, questionID, "session.waiting")
		}(session.ID, recorded.ID)
	}
	return recorded
}

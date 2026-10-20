package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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
// one step.
// A changed record asks the catalog for a discovery pass, so its rows show
// the change without waiting for the next scheduled one.
func (d *Daemon) observeTurn(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, error) {
	defer d.lockWaiting(session.ID)()
	observation, changed, err := d.observeTurnLocked(ctx, adapter, session)
	if changed {
		d.requestCatalogDiscovery()
	}
	return observation, err
}

func (d *Daemon) observeTurnLocked(ctx context.Context, adapter surface.Surface, session *surface.Session) (*surface.TurnObservation, bool, error) {
	observation, err := adapter.Observe(ctx, session)
	if err != nil || observation == nil {
		return observation, false, err
	}
	waiting := observation.WaitingOn
	if observation.Status != surface.StatusBusy {
		waiting = nil
	}
	record, err := d.Registry.RecordWaiting(session.ID, waiting, time.Now())
	if err != nil {
		d.log.Printf("record waiting %s: %s", d.resolveDisplay(session.ID), err)
		return observation, false, nil
	}
	if record.Announce {
		d.announceWaiting(*session, *record.Waiting)
	}
	observation.WaitingOn = record.Waiting
	return observation, record.Changed, nil
}

// catalogWaiting returns what a listed session is waiting on. A listing that
// agrees with the record is taken as it is; one that disagrees is confirmed
// by observing the turn, since a listing may be older than the record. The
// caller holds the session's waiting lock until its catalog row commits.
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
	observation, _, err := d.observeTurnLocked(observeCtx, adapter, &session)
	if err != nil || observation == nil {
		return recorded
	}
	return observation.WaitingOn
}

// announceWaiting tells the user, on the desktop and on paired devices, that
// a session is waiting on them, and opens the conversation they know it by.
// The record is marked announced only after both were attempted, so a daemon
// stopped in between announces the question again after it restarts rather
// than never. Both notifications carry a key derived from the question, so a
// repeat replaces the one already shown instead of adding a second.
func (d *Daemon) announceWaiting(session surface.Session, waiting surface.Waiting) {
	d.announceMu.Lock()
	if d.announcing == nil {
		d.announcing = map[string]string{}
	}
	if d.announcing[session.ID] == waiting.ID {
		d.announceMu.Unlock()
		return
	}
	// A caller may have read the question as unannounced just before an
	// announcement finished. That announcement marks the record before it
	// leaves announcing, so the record read here already shows it.
	if recorded, announced, err := d.Registry.WaitingState(session.ID); err == nil && announced && recorded != nil && recorded.ID == waiting.ID {
		d.announceMu.Unlock()
		return
	}
	d.announcing[session.ID] = waiting.ID
	d.announceMu.Unlock()
	conversationID := session.ConversationID()
	desktopMessage := elsewhereWaitingMessage(d.resolveDisplay(conversationID), waiting.Kind)
	mobileMessage := elsewhereWaitingMessage(notificationSurfaceName(session.Surface), waiting.Kind)
	key := waitingNotificationKey(session.ID, waiting.ID)
	go func(sessionID, questionID string) {
		defer func() {
			d.announceMu.Lock()
			if d.announcing[sessionID] == questionID {
				delete(d.announcing, sessionID)
			}
			d.announceMu.Unlock()
		}()
		notificationCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		mobile := mobileNotification{Title: "Agenthail", Message: mobileMessage, SessionID: conversationID, TurnID: questionID, EventType: "session.waiting", CollapseID: key}
		var actions []surface.RequestAction
		if request, open := d.waitingRequest(notificationCtx, session, waiting); open {
			// A notification's buttons answer the session it names, so one
			// that carries a request names the session that holds it.
			mobile.SessionID = sessionID
			desktopMessage = fmt.Sprintf("%s: %s", d.resolveDisplay(sessionID), request.Prompt())
			mobile.RequestID = request.ID
			actions = request.Actions()
		}
		if err := d.notifyDesktop("Agenthail", desktopMessage, mobile.SessionID, "agenthail-"+key, mobile.RequestID, actions); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		d.notifyPairedDevices(notificationCtx, mobile)
		if err := d.Registry.MarkWaitingAnnounced(sessionID, questionID); err != nil {
			d.log.Printf("mark waiting announced %s: %s", sessionID, err)
		}
	}(session.ID, waiting.ID)
}

// waitingRequest is the open request a session waits on, when its provider
// takes answers through Agenthail, read now so the notification shows what is
// actually asked.
func (d *Daemon) waitingRequest(ctx context.Context, session surface.Session, waiting surface.Waiting) (surface.Request, bool) {
	adapter, err := d.adapters().For(&session)
	if err != nil {
		return surface.Request{}, false
	}
	responder, ok := adapter.(surface.Responder)
	if !ok || !surface.EffectiveCapabilities(adapter, &session).Respond {
		return surface.Request{}, false
	}
	requestCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	state, err := responder.Requests(requestCtx, &session)
	if err != nil {
		d.log.Printf("read the request %s waits on: %s", d.resolveDisplay(session.ID), err)
		return surface.Request{}, false
	}
	return openRequest(state, waiting.ID)
}

// elsewhereWaitingMessage says what a session waits on when Agenthail
// cannot read the prompt itself.
func elsewhereWaitingMessage(name string, kind surface.WaitingKind) string {
	switch kind {
	case surface.WaitingQuestion:
		return fmt.Sprintf("%s asks you a question", name)
	case surface.WaitingInput:
		return fmt.Sprintf("%s needs your input", name)
	default:
		return fmt.Sprintf("%s needs your approval", name)
	}
}

// waitingNotificationKey names one question's notifications within the 64
// bytes APNs allows for a collapse id.
func waitingNotificationKey(sessionID, questionID string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + questionID))
	return "waiting-" + hex.EncodeToString(sum[:16])
}

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
	}
	return observation, err
}

// publishWaiting writes a session's catalog row with what it waits on now,
// through the entry the last discovery pass left for status refreshes. A
// session without one, or whose row did not commit, waits for a discovery
// pass instead.
func (d *Daemon) publishWaiting(ctx context.Context, sessionID string, waiting *surface.Waiting) {
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
	session.WaitingOn = waiting
	if waiting != nil {
		session.Status = surface.StatusBusy
	}
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
	waiting := observation.WaitingOn
	if observation.Status != surface.StatusBusy {
		waiting = nil
	}
	record, err := d.Registry.RecordWaiting(session.ID, waiting, time.Now())
	if err != nil {
		d.log.Printf("record waiting %s: %s", d.resolveDisplay(session.ID), err)
		return observation, false, nil
	}
	if record.Replaced != nil && record.ReplacedAnnounced {
		d.withdrawWaiting(session.ID, *record.Replaced)
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
	restartable := false
	if adapter, err := d.adapters().For(&session); err == nil {
		lifecycle := surface.EffectiveCapabilities(adapter, &session).Lifecycle
		restartable = lifecycle.Stop && lifecycle.Resume
	}
	desktopMessage := elsewhereWaitingMessage(d.resolveDisplay(conversationID), waiting, session.RuntimeKind, restartable)
	mobileMessage := elsewhereWaitingMessage(notificationSurfaceName(session.Surface), waiting, session.RuntimeKind, restartable)
	key := waitingNotificationKey(session.ID, waiting.ID)
	sessionID, questionID := session.ID, waiting.ID
	finished := func() {
		d.announceMu.Lock()
		if d.announcing[sessionID] == questionID {
			delete(d.announcing, sessionID)
		}
		d.announceMu.Unlock()
	}
	started := d.background.start(func(ctx context.Context) {
		defer finished()
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
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
		// The desktop notification goes through a helper app that can take
		// seconds to start, so the phone is told alongside it.
		var desktop sync.WaitGroup
		desktop.Add(1)
		go func() {
			defer desktop.Done()
			if err := d.notifyDesktop("Agenthail", desktopMessage, mobile.SessionID, "agenthail-"+key, mobile.RequestID, actions); err != nil {
				d.log.Printf("desktop notification: %s", err)
			}
		}()
		d.notifyPairedDevices(notificationCtx, mobile)
		desktop.Wait()
		if ctx.Err() != nil {
			return
		}
		if err := d.Registry.MarkWaitingAnnounced(sessionID, questionID); err != nil {
			d.log.Printf("mark waiting announced %s: %s", sessionID, err)
		}
		// A question settled while it was being announced was not withdrawn
		// with the record, which had not been marked announced yet.
		if recorded, _, err := d.Registry.WaitingState(sessionID); err == nil && (recorded == nil || recorded.ID != questionID) {
			d.withdrawWaitingNow(ctx, sessionID, waiting)
		}
	})
	if !started {
		finished()
	}
}

// withdrawWaiting removes the notifications that told the user about a
// question that is no longer waiting, on the desktop and on paired devices,
// so a question answered anywhere stops asking everywhere.
func (d *Daemon) withdrawWaiting(sessionID string, waiting surface.Waiting) {
	d.background.start(func(ctx context.Context) {
		d.withdrawWaitingNow(ctx, sessionID, waiting)
	})
}

func (d *Daemon) withdrawWaitingNow(ctx context.Context, sessionID string, waiting surface.Waiting) {
	key := waitingNotificationKey(sessionID, waiting.ID)
	if err := d.withdrawDesktop("agenthail-" + key); err != nil {
		d.log.Printf("withdraw desktop notification: %s", err)
	}
	withdrawCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d.withdrawFromPairedDevices(withdrawCtx, key)
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
		}
	}
	return fmt.Sprintf("%s %s", name, asks)
}

// waitingNotificationKey names one question's notifications within the 64
// bytes APNs allows for a collapse id.
func waitingNotificationKey(sessionID, questionID string) string {
	sum := sha256.Sum256([]byte(sessionID + "\x00" + questionID))
	return "waiting-" + hex.EncodeToString(sum[:16])
}

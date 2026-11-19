package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Codex starts no turn when background work ends, so a held turn completes when that work stops.
func heldCompletion(previous registry.RuntimeState, observation *surface.TurnObservation, completionChanged bool) (held string, released bool) {
	backgroundOnly := observation.ActiveTurnID == "" && surface.BackgroundRunning(observation.BackgroundTasks)
	if completionChanged {
		if backgroundOnly {
			return observation.CompletedTurnID, false
		}
		return "", false
	}
	if previous.HeldCompletionTurnID == "" || observation.ActiveTurnID != "" || observation.CompletedTurnID != previous.HeldCompletionTurnID {
		return "", false
	}
	if backgroundOnly {
		return previous.HeldCompletionTurnID, false
	}
	return "", true
}

func (d *Daemon) readUnobservedBackground(ctx context.Context, adapter surface.Surface, session *surface.Session) bool {
	source, ok := adapter.(surface.BackgroundSource)
	if !ok {
		return false
	}
	session.BackgroundTasks = source.Background(ctx, *session)
	return true
}

func (d *Daemon) releaseUnobservedHold(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	previous, found, err := d.Registry.RuntimeState(session.ID)
	if err != nil || !found || previous.HeldCompletionTurnID == "" || surface.BackgroundRunning(session.BackgroundTasks) {
		return
	}
	if err := d.Registry.HoldCompletion(session.ID, ""); err != nil {
		d.log.Printf("release held completion %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if err := d.Registry.ClearCompletionNotification(session.ID); err != nil {
		d.log.Printf("clear completion notification %s: %s", d.resolveDisplay(session.ID), err)
	}
	if completionNotificationEligible(previous, d.consumeNotificationArm(session.ID)) {
		turnID := previous.HeldCompletionTurnID
		var noticed *turnCompletedNotice
		if !session.Delegated() {
			notice := d.endTurnNotifications(session, turnID, fmt.Sprintf("%s finished", d.resolveDisplay(session.ID)), fmt.Sprintf("%s finished%s", d.sessionPushSubject(session), d.onHost()))
			noticed = &notice
		}
		switch {
		case noticed == nil:
			d.recordCompletion(session, turnID, false, nil)
			d.resumeTurnPush(session.ID)
		case noticed.quiet:
			d.recordCompletion(session, turnID, false, noticed)
			d.dropTurnPush(session.ID)
		default:
			d.recordCompletion(session, turnID, false, nil)
			d.scheduleTurnPush(session.ID, turnID, *noticed)
		}
	}
}

func (d *Daemon) notifyTurnCompleted(notice turnCompletedNotice, sessionID, turnID string) {
	d.background.start(func(ctx context.Context) {
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.deliverTurnCompleted(notificationCtx, notice, sessionID, turnID)
	})
}

func (d *Daemon) deliverTurnCompleted(ctx context.Context, notice turnCompletedNotice, sessionID, turnID string) {
	hostID := d.hostIdentity().ID
	if err := d.notifyDesktop("Agenthail", notice.desktop, sessionID, "", notice.requestID, notice.actions); err != nil {
		d.log.Printf("desktop notification: %s", err)
	}
	d.notifyPairedDevices(ctx, mobileNotification{Title: "Agenthail", Message: notice.mobile, SessionID: sessionID, TurnID: turnID, EventType: "turn.completed", RequestID: notice.requestID, CollapseID: turnNotificationKey(hostID, sessionID, turnID), DedupeKey: completionDedupeKey(hostID, sessionID, turnID)})
}

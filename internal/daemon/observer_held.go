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

func (d *Daemon) releaseUnobservedHold(session *surface.Session) {
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
	if completionNotificationEligible(previous, d.consumeNotificationArm(session.ID)) && !session.Delegated() {
		d.notifyTurnCompleted(fmt.Sprintf("%s finished", d.resolveDisplay(session.ID)), fmt.Sprintf("%s finished", notificationSurfaceName(session.Surface)), session.ID, previous.HeldCompletionTurnID)
	}
}

func (d *Daemon) notifyTurnCompleted(desktopMessage, mobileMessage, sessionID, turnID string) {
	d.background.start(func(ctx context.Context) {
		if err := d.notifyDesktop("Agenthail", desktopMessage, sessionID, "", "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, mobileNotification{Title: "Agenthail", Message: mobileMessage, SessionID: sessionID, TurnID: turnID, EventType: "turn.completed"})
	})
}

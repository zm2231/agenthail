package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	turnOutcomeFinished = "finished"
	turnOutcomeFailed   = "failed"
	turnOutcomeNeedsYou = "needs_you"
)

// A viewing host notifies from its host's notice and never judges a mirrored turn finished itself.
type dashboardTurnCompletion struct {
	TurnID    string    `json:"turnId"`
	Outcome   string    `json:"outcome"`
	RequestID string    `json:"requestId,omitempty"`
	At        time.Time `json:"at"`
}

func turnCompletionRow(notice registry.TurnNotice) *dashboardTurnCompletion {
	return &dashboardTurnCompletion{TurnID: notice.TurnID, Outcome: notice.Outcome, RequestID: notice.RequestID, At: notice.At}
}

// Identical on the host and every viewer, so the relay delivers it to a phone once.
func completionDedupeKey(hostID, sessionID, turnID string) string {
	if hostID == "" || sessionID == "" || turnID == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(hostID + "\x00" + sessionID + "\x00" + turnID))
	return hex.EncodeToString(sum[:])
}

// A phone paired to this host and a viewer may show this push instead of the viewer's.
func (d *Daemon) onHost() string {
	viewed, err := d.Registry.HasHostViewers()
	if err != nil {
		d.log.Printf("read host viewers: %s", err)
	}
	if name := d.hostIdentity().Name; viewed && name != "" {
		return " on " + name
	}
	return ""
}

type mirroredRow struct {
	TurnCompletion *dashboardTurnCompletion `json:"turnCompletion"`
	Ask            *dashboardAsk            `json:"ask"`
}

func mirroredState(projection string) mirroredRow {
	var row mirroredRow
	_ = json.Unmarshal([]byte(projection), &row)
	return row
}

// Only live catalog stream rows reach this; snapshots, taken on every connect, only record.
// A turn that leaves an ask for the person stays quiet: the ask is announced on its own.
func (d *Daemon) notifyMirroredCompletion(host *pairedHost, rawID string, row dashboardSession, prior *dashboardTurnCompletion) {
	completion := row.TurnCompletion
	if completion == nil || prior != nil && prior.TurnID == completion.TurnID || completion.Outcome == turnOutcomeNeedsYou {
		return
	}
	status := "finished"
	if completion.Outcome == turnOutcomeFailed {
		status = "failed"
	}
	message := fmt.Sprintf("%s %s on %s", notificationSurfaceName(row.Surface), status, host.record.Name)
	notification := mobileNotification{Title: "Agenthail", Message: message, SessionID: row.ID, TurnID: completion.TurnID, EventType: "turn.completed", DedupeKey: completionDedupeKey(host.record.ID, rawID, completion.TurnID)}
	d.background.start(func(ctx context.Context) {
		if err := d.notifyDesktop("Agenthail", message, row.ID, "", "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, notification)
	})
}

// An ask that reaches the person on a paired host is announced here too, under the key the holding host uses, and withdrawn once answered.
func (d *Daemon) notifyMirroredAsk(host *pairedHost, row dashboardSession, prior *dashboardAsk) {
	ask := row.Ask
	if prior.asksPerson() && (ask == nil || ask.ID != prior.ID || !ask.asksPerson()) {
		d.withdrawAsk(prior.ID)
	}
	if !ask.asksPerson() || prior != nil && prior.ID == ask.ID && prior.asksPerson() {
		return
	}
	desktop := fmt.Sprintf("%s needs you on %s: %s", d.resolveDisplay(row.ID), host.record.Name, ask.Title)
	mobile := mobileNotification{Title: "Agenthail", Message: fmt.Sprintf("%s needs you on %s: %s", notificationSurfaceName(row.Surface), host.record.Name, ask.Title), SessionID: row.ID, TurnID: ask.ID, EventType: "ask.opened",
		CollapseID: askNotificationKey(ask.ID), RequestID: ask.ID, DedupeKey: askDedupeKey(host.record.ID, ask.ID)}
	d.sendAskNotifications(desktop, mobile, nil)
}

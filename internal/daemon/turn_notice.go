package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
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

func (d *Daemon) recordTurnNotice(sessionID, turnID string, notice turnCompletedNotice) {
	if err := d.Registry.RecordTurnNotice(sessionID, registry.TurnNotice{TurnID: turnID, Outcome: notice.outcome, RequestID: notice.requestID, At: time.Now()}); err != nil {
		d.log.Printf("record turn notice %s: %s", d.resolveDisplay(sessionID), err)
		return
	}
	d.requestCatalogDiscovery()
}

func mirroredTurnCompletion(projection string) *dashboardTurnCompletion {
	var row struct {
		TurnCompletion *dashboardTurnCompletion `json:"turnCompletion"`
	}
	if json.Unmarshal([]byte(projection), &row) != nil {
		return nil
	}
	return row.TurnCompletion
}

// Only live catalog stream rows reach this; snapshots, taken on every connect, only record.
func (d *Daemon) notifyMirroredCompletion(host *pairedHost, rawID string, row dashboardSession, prior *dashboardTurnCompletion) {
	completion := row.TurnCompletion
	if completion == nil || prior != nil && prior.TurnID == completion.TurnID {
		return
	}
	status := "finished"
	switch completion.Outcome {
	case turnOutcomeFailed:
		status = "failed"
	case turnOutcomeNeedsYou:
		status = "needs you"
	}
	message := fmt.Sprintf("%s %s on %s", notificationSurfaceName(row.Surface), status, host.record.Name)
	notification := mobileNotification{Title: "Agenthail", Message: message, SessionID: row.ID, TurnID: completion.TurnID, EventType: "turn.completed", DedupeKey: completionDedupeKey(host.record.ID, rawID, completion.TurnID)}
	var actions []surface.RequestAction
	if completion.Outcome == turnOutcomeNeedsYou && completion.RequestID != "" && row.EndTurn != nil && row.EndTurn.RequestID == completion.RequestID {
		request := turnEndRequest(registry.TurnEnd{Needs: row.EndTurn.Needs, Headline: row.EndTurn.Headline, Asks: row.EndTurn.Asks, EndedAt: row.EndTurn.EndedAt})
		notification.RequestID, actions = completion.RequestID, request.Actions()
	}
	d.background.start(func(ctx context.Context) {
		if err := d.notifyDesktop("Agenthail", message, row.ID, "", notification.RequestID, actions); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, notification)
	})
}

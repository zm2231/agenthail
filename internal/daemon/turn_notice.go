package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/url"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
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

type mirroredRow struct {
	TurnCompletion *dashboardTurnCompletion `json:"turnCompletion"`
	Ask            *dashboardAsk            `json:"ask"`
	TurnState      *dashboardTurnState      `json:"turnState"`
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
	body := "Finished"
	if completion.Outcome == turnOutcomeFailed {
		body = "Failed"
	}
	title := pushTag(row.Surface, row.Alias, row.Name)
	notification := mobileNotification{Title: title, Message: body, SessionID: row.ID, TurnID: completion.TurnID, EventType: "turn.completed", CollapseID: turnNotificationKey(host.record.ID, rawID, completion.TurnID), DedupeKey: completionDedupeKey(host.record.ID, rawID, completion.TurnID)}
	d.background.start(func(ctx context.Context) {
		if err := d.notifyDesktop(title, body, row.ID, "", "", nil); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, notification)
	})
}

// Announced under the holding host's dedupe key; the row has no questions, so buttons come from the host's requests when it answers.
func (d *Daemon) notifyMirroredAsk(host *pairedHost, rawID string, row dashboardSession, prior *dashboardAsk) {
	ask := row.Ask
	if prior.asksPerson() && (ask == nil || ask.ID != prior.ID || !ask.asksPerson()) {
		d.withdrawAsk(prior.ID)
	}
	if !ask.asksPerson() || prior != nil && prior.ID == ask.ID && prior.asksPerson() {
		return
	}
	desktop := "Needs you: " + ask.Title
	mobile := mobileNotification{Title: pushTag(row.Surface, row.Alias, row.Name), Message: desktop, SessionID: row.ID, TurnID: ask.ID, EventType: "ask.opened",
		CollapseID: askNotificationKey(ask.ID), RequestID: ask.ID, DedupeKey: askDedupeKey(host.record.ID, ask.ID)}
	d.background.start(func(ctx context.Context) {
		d.deliverAskNotifications(ctx, desktop, mobile, d.mirroredAskActions(ctx, host, rawID, ask.ID))
	})
}

func (d *Daemon) mirroredAskActions(ctx context.Context, host *pairedHost, rawID, askID string) []surface.RequestAction {
	readCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	response, err := host.client.Do(readCtx, http.MethodGet, "/api/v1/requests", url.Values{"sessionId": {rawID}}, nil, nil)
	if err != nil {
		d.log.Printf("read %s on %s for its buttons: %s", askID, host.record.Name, err)
		return nil
	}
	body, err := hosts.ReadResponse(response)
	if err != nil || response.StatusCode != http.StatusOK {
		d.log.Printf("read %s on %s for its buttons: %d %v", askID, host.record.Name, response.StatusCode, err)
		return nil
	}
	var listed struct {
		Requests []openRequestView `json:"requests"`
	}
	if err := json.Unmarshal(body, &listed); err != nil {
		d.log.Printf("read %s on %s for its buttons: %s", askID, host.record.Name, err)
		return nil
	}
	for _, request := range listed.Requests {
		if request.ID == askID {
			return request.Actions
		}
	}
	return nil
}

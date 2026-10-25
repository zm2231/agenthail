package daemon

import (
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) writeUnstartedSession(w http.ResponseWriter, message string, startErr error) {
	unknown := surface.IsDeliveryOutcomeUnknown(startErr)
	status, code := "failed", "create_failed"
	if unknown {
		status, code = "unknown", "create_unknown"
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: status, Message: message, Error: startErr.Error()})
	detail := startErr.Error()
	if unknown {
		detail = fmt.Sprintf("initial turn outcome is ambiguous, but no session identity was returned; inspect the provider before any explicit retry: %s", startErr)
	}
	writeAPIError(w, http.StatusBadGateway, code, detail)
}

func (d *Daemon) writeStartedSessionOutcome(w http.ResponseWriter, session *surface.Session, creator, alias, messageUUID, message string, sent *surface.SendResult, startErr error, extra map[string]any) {
	if startErr != nil {
		intent, intentErr := d.Registry.RecordSessionCreationIntent(session.ID, surface.SendOptions{SourceSessionID: creator, Author: surface.AuthorPerson}.Sender(), messageUUID, message)
		if intentErr != nil {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: fmt.Sprintf("%s; durable delivery intent failed: %s", startErr, intentErr)})
			writeCreateStorageFailure(w, session, fmt.Sprintf("session was created, but its delivery intent could not be persisted: %s; do not retry automatically", intentErr), extra)
			return
		}
		if surface.IsDeliveryOutcomeUnknown(startErr) {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "submitted", SessionID: session.ID, Message: message, Error: startErr.Error()})
			writeSubmittedSession(w, session, intent.ID, submittedSessionDetail(session, alias), extra)
			return
		}
		_, noticeErr := d.Registry.FailDeliveryIntentWithNotice(intent.ID, startErr.Error())
		if noticeErr != nil {
			d.log.Printf("queue initial delivery failure notice for %s: %s", session.ID, noticeErr)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: startErr.Error()})
		if err := d.catalog.flushCommitted(); err != nil {
			d.log.Printf("publish initial delivery failure: %s", err)
		}
		writeInitialSessionFailure(w, session, intent.ID, startErr.Error(), extra)
		return
	}
	result := ""
	if sent != nil {
		result = sent.UUID
	}
	if result != "" {
		if runtimeErr := d.Registry.MarkDeliveryStarted(session.ID, result, ""); runtimeErr != nil {
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: message, Result: result, Error: runtimeErr.Error()})
		}
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: message, Result: result})
	writeSessionCreateJSON(w, http.StatusCreated, map[string]any{"ok": true, "session": session, "result": sent}, extra)
}

func writeSessionCreateJSON(w http.ResponseWriter, status int, body, extra map[string]any) {
	for key, value := range extra {
		body[key] = value
	}
	writeDashboardJSON(w, status, body)
}

func writeSubmittedSession(w http.ResponseWriter, session *surface.Session, deliveryID int64, detail string, extra map[string]any) {
	body := map[string]any{
		"ok":        true,
		"status":    "submitted",
		"accepted":  true,
		"retryable": false,
		"session":   session,
		"detail":    detail,
	}
	if deliveryID > 0 {
		body["deliveryId"] = deliveryID
	}
	writeSessionCreateJSON(w, http.StatusAccepted, body, extra)
}

func submittedSessionDetail(session *surface.Session, alias string) string {
	target := alias
	if target != "" {
		target = "@" + target
	} else {
		target = fmt.Sprintf("%s:%s", session.Surface, session.ID)
	}
	return "Submitted to " + target + "."
}

func writeInitialSessionFailure(w http.ResponseWriter, session *surface.Session, deliveryID int64, message string, extra map[string]any) {
	body := map[string]any{"session": session, "deliveryId": deliveryID}
	for key, value := range extra {
		body[key] = value
	}
	writeAPIErrorWith(w, http.StatusBadGateway, "initial_turn_failed", message, body)
}

func writeCreateStorageFailure(w http.ResponseWriter, session *surface.Session, warning string, extra map[string]any) {
	body := map[string]any{"session": session}
	for key, value := range extra {
		body[key] = value
	}
	writeAPIErrorWith(w, http.StatusInternalServerError, "create_storage_failed", warning, body)
}

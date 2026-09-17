package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type zenActionRequest struct {
	Action          string `json:"action"`
	SessionID       string `json:"sessionId"`
	IdempotencyKey  string `json:"idempotencyKey"`
	Message         string `json:"message"`
	SourceSessionID string `json:"sourceSessionId"`
}

type zenReceipt struct {
	Disposition string `json:"disposition"`
	Detail      string `json:"detail,omitempty"`
	TurnID      string `json:"turnId,omitempty"`
	QueueID     int64  `json:"queueId,omitempty"`
}

type zenActionResponse struct {
	Receipt zenReceipt `json:"receipt"`
}

type zenGatewayError struct {
	Status int
	Code   string
	Err    error
}

func (e zenGatewayError) Error() string { return e.Err.Error() }
func (e zenGatewayError) Unwrap() error { return e.Err }

func (d *Daemon) handleZENAction(w http.ResponseWriter, r *http.Request, request zenActionRequest) {
	request.Action = strings.TrimSpace(request.Action)
	request.SessionID = strings.TrimSpace(request.SessionID)
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.SourceSessionID = strings.TrimSpace(request.SourceSessionID)
	if request.IdempotencyKey == "" || request.SessionID == "" {
		writeZENError(w, zenGatewayError{Status: http.StatusBadRequest, Code: "invalid_request", Err: errors.New("sessionId and idempotencyKey are required")})
		return
	}
	if request.Action != "send" && request.Action != "steer" && request.Action != "interrupt" {
		writeZENError(w, zenGatewayError{Status: http.StatusUnprocessableEntity, Code: "unsupported_action", Err: fmt.Errorf("action %q is not supported", request.Action)})
		return
	}
	if request.Action != "interrupt" && strings.TrimSpace(request.Message) == "" {
		writeZENError(w, zenGatewayError{Status: http.StatusBadRequest, Code: "invalid_request", Err: errors.New("message is required")})
		return
	}
	if strings.HasPrefix(request.SourceSessionID, "zen:") {
		if strings.TrimPrefix(request.SourceSessionID, "zen:") == "" || !d.zenSourceAuthorized(r) {
			writeZENError(w, zenGatewayError{Status: http.StatusForbidden, Code: "source_not_authorized", Err: errors.New("ZEN source attribution requires read, control, and zen scopes")})
			return
		}
	} else if request.SourceSessionID != "" {
		if _, err := d.Registry.Session(request.SourceSessionID); err != nil {
			writeZENError(w, zenGatewayError{Status: http.StatusNotFound, Code: "source_session_not_found", Err: fmt.Errorf("source session %q was not found", request.SourceSessionID)})
			return
		}
	}
	if reservation, err := d.Registry.APIAction(request.IdempotencyKey); err != nil {
		writeZENError(w, zenGatewayError{Status: http.StatusInternalServerError, Code: "reservation_failed", Err: fmt.Errorf("load action reservation: %w", err)})
		return
	} else if reservation != nil {
		if !sameZENEnvelope(reservation, request) {
			writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "idempotency_conflict", Err: errors.New("idempotencyKey was already used for a different action")})
			return
		}
		writeZENReceipt(w, replayZENReceipt(reservation))
		return
	}
	session, err := d.Registry.Session(request.SessionID)
	if err != nil {
		writeZENError(w, zenGatewayError{Status: http.StatusNotFound, Code: "session_not_found", Err: fmt.Errorf("session %q was not found", request.SessionID)})
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	if adapter == nil {
		writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "surface_unavailable", Err: errors.New("session surface is not configured")})
		return
	}
	if err := surface.EnsureWritableSession(r.Context(), adapter, session); err != nil {
		writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "session_not_writable", Err: err})
		return
	}
	if request.Action == "send" && !adapter.Capabilities().Send {
		writeZENError(w, zenGatewayError{Status: http.StatusUnprocessableEntity, Code: "unsupported_action", Err: errors.New("this session cannot receive messages")})
		return
	}
	if request.Action == "steer" && !adapter.Capabilities().Steer {
		writeZENError(w, zenGatewayError{Status: http.StatusUnprocessableEntity, Code: "unsupported_action", Err: errors.New("this session cannot be steered")})
		return
	}
	if request.Action == "interrupt" && !adapter.Capabilities().Interrupt {
		writeZENError(w, zenGatewayError{Status: http.StatusUnprocessableEntity, Code: "unsupported_action", Err: errors.New("this session cannot be interrupted")})
		return
	}
	reservation, created, err := d.Registry.ReserveAPIAction(request.IdempotencyKey, request.SessionID, request.Action, request.Message, request.SourceSessionID)
	if err != nil {
		writeZENError(w, zenGatewayError{Status: http.StatusInternalServerError, Code: "reservation_failed", Err: fmt.Errorf("reserve action: %w", err)})
		return
	}
	if !created {
		if !sameZENEnvelope(reservation, request) {
			writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "idempotency_conflict", Err: errors.New("idempotencyKey was already used for a different action")})
			return
		}
		writeZENReceipt(w, replayZENReceipt(reservation))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	receipt, actionErr := d.executeZENAction(ctx, adapter, session, request)
	if actionErr != nil {
		if receipt.Disposition == "failed" {
			if err := d.Registry.CompleteAPIAction(request.IdempotencyKey, "failed", receipt); err != nil {
				writeZENError(w, zenGatewayError{Status: http.StatusInternalServerError, Code: "reservation_failed", Err: fmt.Errorf("record failed action: %w", err)})
				return
			}
			writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "delivery_rejected", Err: actionErr})
			return
		}
		receipt = zenReceipt{Disposition: "unknown", Detail: actionErr.Error()}
		_ = d.Registry.CompleteAPIAction(request.IdempotencyKey, "unknown", receipt)
		writeZENReceipt(w, receipt)
		return
	}
	if err := d.Registry.CompleteAPIAction(request.IdempotencyKey, receipt.Disposition, receipt); err != nil {
		receipt = zenReceipt{Disposition: "unknown", Detail: "action receipt could not be durably recorded"}
		writeZENReceipt(w, receipt)
		return
	}
	writeZENReceipt(w, receipt)
}

func sameZENEnvelope(reservation *registry.APIActionReservation, request zenActionRequest) bool {
	return reservation != nil && reservation.SessionID == request.SessionID && reservation.Action == request.Action && reservation.Message == request.Message && reservation.SourceSessionID == request.SourceSessionID
}

func (d *Daemon) executeZENAction(ctx context.Context, adapter surface.Surface, session *surface.Session, request zenActionRequest) (zenReceipt, error) {
	if request.Action == "send" {
		receipt, err := (delivery.Dispatcher{Registry: d.Registry}).DeliverWithOptions(ctx, adapter, session, request.Message, request.IdempotencyKey, surface.SendOptions{SourceSessionID: request.SourceSessionID})
		if err != nil {
			if surface.IsDeliveryTerminal(err) {
				return zenReceipt{Disposition: "failed", Detail: err.Error()}, err
			}
			return zenReceipt{}, err
		}
		result := zenReceipt{Disposition: string(receipt.Disposition), TurnID: receipt.TurnID, QueueID: receipt.QueueID, Detail: receipt.Reason}
		return result, nil
	}
	ctx = surface.WithSourceSessionID(ctx, request.SourceSessionID)
	if request.Action == "steer" {
		if err := adapter.Steer(ctx, session, request.Message); err != nil {
			return zenReceipt{}, err
		}
	} else if err := adapter.Interrupt(ctx, session); err != nil {
		return zenReceipt{}, err
	}
	return zenReceipt{Disposition: "accepted"}, nil
}

func replayZENReceipt(reservation *registry.APIActionReservation) zenReceipt {
	if reservation.Status == "complete" || reservation.Status == "accepted" || reservation.Status == "queued" {
		var receipt zenReceipt
		if json.Unmarshal(reservation.Receipt, &receipt) == nil && receipt.Disposition != "" {
			return receipt
		}
	}
	if reservation.Status == "failed" {
		return zenReceipt{Disposition: "failed", Detail: "previous action failed before delivery"}
	}
	return zenReceipt{Disposition: "unknown", Detail: "action outcome requires reconciliation"}
}

func (d *Daemon) zenSourceAuthorized(r *http.Request) bool {
	token := bearerToken(r)
	if token == "" {
		return false
	}
	if d.dashboard != nil && constantTokenEqual(token, d.dashboard.token) {
		return true
	}
	if _, err := d.Registry.AuthenticateDevice(token, "read"); err != nil {
		return false
	}
	_, err := d.Registry.AuthenticateDevice(token, "zen")
	return err == nil
}

func writeZENReceipt(w http.ResponseWriter, receipt zenReceipt) {
	status := http.StatusOK
	if receipt.Disposition == "queued" || receipt.Disposition == "unknown" {
		status = http.StatusAccepted
	}
	writeDashboardJSON(w, status, zenActionResponse{Receipt: receipt})
}

func writeZENError(w http.ResponseWriter, err zenGatewayError) {
	writeAPIError(w, err.Status, err.Code, err.Error())
}

func (d *Daemon) apiSessionStreamHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	if err := d.events.journalError(); err != nil {
		writeAPIError(w, http.StatusServiceUnavailable, "event_journal_unavailable", err.Error())
		return
	}
	sessionID := strings.TrimSpace(r.URL.Query().Get("id"))
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", fmt.Sprintf("session %q was not found", sessionID))
		return
	}
	adapter := d.surfaceForKind(session.Surface)
	_, providerOK := adapter.(surface.TimelineProvider)
	if !providerOK || !surface.EffectiveCapabilities(session, adapter.Capabilities()).Stream {
		writeAPIError(w, http.StatusConflict, "stream_unsupported", "this session does not expose a replayable structured stream")
		return
	}
	after, valid := parseSessionStreamCursor(r)
	if !valid {
		writeAPIError(w, http.StatusConflict, "stream_gap", "Last-Event-ID is invalid or unavailable; request a full replay")
		return
	}
	initialCtx, initialCancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	if err := d.publishTimelineEvents(initialCtx, adapter, session); err != nil {
		initialCancel()
		writeZENError(w, zenGatewayError{Status: http.StatusServiceUnavailable, Code: "stream_unavailable", Err: fmt.Errorf("initial timeline replay failed: %w", err)})
		return
	}
	initialCancel()
	backlog, events, reset, cancel := d.events.subscribe(after)
	defer cancel()
	if reset || (after > d.events.cursor() && after != 0) {
		writeZENError(w, zenGatewayError{Status: http.StatusConflict, Code: "stream_gap", Err: errors.New("Last-Event-ID is no longer retained; request a full replay")})
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	for _, event := range backlog {
		if !d.apiEventStreamAuthorized(r) {
			return
		}
		if runtime, ok := sessionRuntimeEvent(event, sessionID); ok {
			writeRuntimeSSEEvent(w, event.ID, runtime)
		}
	}
	flusher.Flush()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
	activityPoll := time.NewTicker(time.Second)
	defer activityPoll.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case event, open := <-events:
			if !open {
				return
			}
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			if runtime, ok := sessionRuntimeEvent(event, sessionID); ok {
				writeRuntimeSSEEvent(w, event.ID, runtime)
				flusher.Flush()
			}
		case <-keepalive.C:
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case <-activityPoll.C:
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			pollCtx, pollCancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
			if err := d.publishTimelineEvents(pollCtx, adapter, session); err != nil {
				writeRuntimeSSEError(w, "stream_unavailable", fmt.Sprintf("timeline replay failed: %s", err))
				flusher.Flush()
				pollCancel()
				return
			}
			pollCancel()
		}
	}
}

func parseSessionStreamCursor(r *http.Request) (uint64, bool) {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	if value == "" {
		return 0, true
	}
	cursor, err := strconv.ParseUint(value, 10, 64)
	return cursor, err == nil
}

func sessionRuntimeEvent(event apiEvent, sessionID string) (map[string]any, bool) {
	if event.EntityID != sessionID {
		return nil, false
	}
	data := map[string]any{}
	if len(event.Data) > 0 && json.Unmarshal(event.Data, &data) != nil {
		return nil, false
	}
	switch event.Type {
	case "message", "thought", "tool_start", "tool_done":
		var runtime canonicalRuntimeEvent
		if json.Unmarshal(event.Data, &runtime) != nil || runtime.Type != event.Type {
			return nil, false
		}
		data := map[string]any{}
		encoded, _ := json.Marshal(runtime)
		if json.Unmarshal(encoded, &data) != nil {
			return nil, false
		}
		return data, true
	case "session.updated":
		data = map[string]any{"type": "phase", "phase": runtimePhase(strValue(data["status"])), "reason": "observed", "sessionId": sessionID}
		data["sessionId"] = sessionID
	case "turn.completed":
		data = map[string]any{"type": "phase", "phase": "idle", "reason": "completed", "sessionId": sessionID}
	default:
		return nil, false
	}
	return data, true
}

func strValue(value any) string {
	result, _ := value.(string)
	return result
}

func runtimePhase(status string) string {
	switch status {
	case "busy", "starting", "running":
		return "running"
	case "offline", "stopped", "failed":
		return "stopped"
	default:
		return "idle"
	}
}

func writeRuntimeSSEEvent(w interface{ Write([]byte) (int, error) }, id uint64, event map[string]any) {
	payload, _ := json.Marshal(map[string]any{"event": event})
	fmt.Fprintf(w, "id: %d\nevent: runtime_event\ndata: %s\n\n", id, payload)
}

func writeRuntimeSSEError(w interface{ Write([]byte) (int, error) }, code, message string) {
	payload, _ := json.Marshal(map[string]any{"error": map[string]string{"code": code, "message": message}})
	fmt.Fprintf(w, "event: stream_error\ndata: %s\n\n", payload)
}

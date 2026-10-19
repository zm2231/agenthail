package daemon

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// requestsHandler reads a session's open requests and permission mode from
// its provider now. Answers go through the respond action.
func (d *Daemon) requestsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	session, err := d.Registry.Session(r.URL.Query().Get("sessionId"))
	if err != nil {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		http.Error(w, "surface is not configured", http.StatusConflict)
		return
	}
	responder, ok := adapter.(surface.Responder)
	if !ok || !surface.EffectiveCapabilities(adapter, session).Respond {
		http.Error(w, "this session's requests are not answered through Agenthail", http.StatusBadRequest)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), surfaceOperationTimeout)
	defer cancel()
	state, err := responder.Requests(ctx, session)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"sessionId": session.ID, "permissionMode": state.PermissionMode, "requests": state.Requests})
}

// respond answers one open request and records who answered it. A request
// that was already answered or has expired is a conflict, never a retry. The
// turn is observed again right away so the session stops showing it.
func (d *Daemon) respond(ctx context.Context, w http.ResponseWriter, adapter surface.Surface, session *surface.Session, requestID string, decision surface.Decision, actor string) {
	responder, ok := adapter.(surface.Responder)
	if !ok || !surface.EffectiveCapabilities(adapter, session).Respond {
		http.Error(w, "this session's requests are not answered through Agenthail", http.StatusBadRequest)
		return
	}
	outcome, err := responder.Respond(ctx, session, requestID, decision)
	if errors.Is(err, surface.ErrRequestStale) {
		writeDashboardJSON(w, http.StatusConflict, map[string]any{"ok": false, "error": map[string]string{"code": "stale", "message": surface.ErrRequestStale.Error()}})
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "request-" + outcome, SessionID: session.ID, Message: requestID, Result: "answered by " + actor})
	observeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	if _, err := d.observeTurn(observeCtx, adapter, session); err != nil {
		d.log.Printf("observe %s after an answer: %s", d.resolveDisplay(session.ID), err)
	}
	cancel()
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "result": map[string]string{"requestId": requestID, "outcome": outcome}})
}

// recordSetting writes a model or permission-mode change into the session's
// history with who made it.
func (d *Daemon) recordSetting(session *surface.Session, action, value, actor string) {
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: action, SessionID: session.ID, Message: value, Result: "changed by " + actor})
}

// actionActor names who sent a request: a paired device or host by name, or
// the dashboard.
func actionActor(r *http.Request) string {
	if principal, ok := r.Context().Value(apiHostPrincipalContextKey{}).(apiHostPrincipal); ok && strings.TrimSpace(principal.hostName) != "" {
		if principal.host {
			return fmt.Sprintf("host %s", principal.hostName)
		}
		return fmt.Sprintf("device %s", principal.hostName)
	}
	return "the dashboard"
}

package daemon

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) zenSessionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	defer r.Body.Close()
	var session surface.Session
	if err := decodeAPIV1JSON(w, r, &session, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The ZEN session registration is invalid.")
		return
	}
	if strings.TrimSpace(session.ID) == "" || session.Surface != surface.KindZen {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A ZEN session id and zen surface are required.")
		return
	}
	if session.Status == "" {
		session.Status = surface.StatusIdle
	}
	if session.LastActive.IsZero() {
		session.LastActive = time.Now().UTC()
	}
	if err := d.Registry.RegisterSession(session); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "registration_failed", err.Error())
		return
	}
	d.publishEvent("harness.session.registered", session.ID, session)
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"session": session})
}

func (d *Daemon) zenSessionEventHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST for this endpoint.")
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/api/v1/zen/sessions/")
	if !strings.HasSuffix(path, "/events") {
		writeAPIError(w, http.StatusNotFound, "not_found", "Unknown ZEN bridge endpoint.")
		return
	}
	sessionID := strings.TrimSuffix(path, "/events")
	if sessionID == "" {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A ZEN session id is required.")
		return
	}
	if _, err := d.Registry.Session(sessionID); err != nil {
		writeAPIError(w, http.StatusNotFound, "session_not_found", "ZEN session is not registered.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Event json.RawMessage `json:"event"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil || len(request.Event) == 0 {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "A structured runtime event is required.")
		return
	}
	d.publishEvent("harness.runtime", sessionID, map[string]any{"sessionId": sessionID, "event": request.Event})
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

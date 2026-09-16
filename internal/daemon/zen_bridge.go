package daemon

import (
	"encoding/json"
	"fmt"
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
	event, err := zenStreamEvent(request.Event)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	d.publishEvent("harness.runtime", sessionID, event)
	writeDashboardJSON(w, http.StatusAccepted, map[string]any{"ok": true})
}

func zenStreamEvent(raw json.RawMessage) (surface.StreamEvent, error) {
	var event struct {
		Type    string        `json:"type"`
		ID      string        `json:"id"`
		Name    string        `json:"name"`
		Text    string        `json:"text"`
		Input   any           `json:"input"`
		Output  any           `json:"output"`
		Error   string        `json:"error"`
		Message string        `json:"message"`
		Reason  string        `json:"reason"`
		Usage   *runtimeUsage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &event); err != nil || strings.TrimSpace(event.Type) == "" {
		return surface.StreamEvent{}, fmt.Errorf("A typed ZEN runtime event is required.")
	}
	var data any
	if err := json.Unmarshal(raw, &data); err != nil {
		return surface.StreamEvent{}, fmt.Errorf("A typed ZEN runtime event is required.")
	}
	stream := surface.StreamEvent{Kind: event.Type, ID: event.ID, Name: event.Name, Text: event.Text, Input: event.Input, Output: event.Output, Error: event.Error, Data: data}
	if stream.Error == "" {
		stream.Error = event.Message
	}
	if stream.Text == "" {
		stream.Text = event.Reason
	}
	if event.Type == "usage" {
		var usage runtimeUsage
		if err := json.Unmarshal(raw, &usage); err != nil {
			return surface.StreamEvent{}, fmt.Errorf("A typed ZEN runtime event is required.")
		}
		stream.Context = &surface.ContextUsage{UsedTokens: usage.Total, CumulativeTokens: usage.Total, InputTokens: usage.Input, OutputTokens: usage.Output}
	}
	if event.Usage != nil {
		stream.Context = &surface.ContextUsage{UsedTokens: event.Usage.Total, CumulativeTokens: event.Usage.Total, InputTokens: event.Usage.Input, OutputTokens: event.Usage.Output}
	}
	return stream, nil
}

type runtimeUsage struct {
	Input  int64 `json:"input"`
	Output int64 `json:"output"`
	Total  int64 `json:"total"`
}

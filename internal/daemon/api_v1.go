package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

type apiDeviceTokenContextKey struct{}

const (
	apiProtocolVersion   = 1
	pairingLifetime      = 5 * time.Minute
	apiRequestBodyLimit  = 140 << 10
	eventKeepalivePeriod = 15 * time.Second
	apiErrorBodyLimit    = 8 << 10
)

type apiVersionResponse struct {
	Protocol        int    `json:"protocol"`
	MinimumProtocol int    `json:"minimumProtocol"`
	MaximumProtocol int    `json:"maximumProtocol"`
	Authentication  string `json:"authentication"`
	EventTransport  string `json:"eventTransport"`
	PushRelayURL    string `json:"pushRelayUrl"`
}

func (d *Daemon) registerAPIV1(mux *http.ServeMux, dashboard *dashboardServer) {
	mux.HandleFunc("/api/v1/version", d.apiV1Guard(dashboard, "read", d.apiVersionHandler))
	mux.HandleFunc("/api/v1/snapshot", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(func(w http.ResponseWriter, r *http.Request) { d.dashboardStateCached(dashboard, w, r) })))
	mux.HandleFunc("/api/v1/events", d.apiV1Guard(dashboard, "read", d.apiEventsHandler))
	mux.HandleFunc("/api/v1/session", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(d.dashboardSessionHandler)))
	mux.HandleFunc("/api/v1/models", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(d.dashboardModelsHandler)))
	mux.HandleFunc("/api/v1/history", d.apiV1Guard(dashboard, "read", apiV1JSONHandler(d.dashboardHistoryHandler)))
	mux.HandleFunc("/api/v1/actions", d.apiV1Guard(dashboard, "control", apiV1JSONHandler(d.dashboardActionHandler)))
	mux.HandleFunc("/api/v1/settings", d.apiV1Guard(dashboard, "settings", apiV1JSONHandler(d.dashboardSettingsHandler)))
	mux.HandleFunc("/api/v1/pairings", d.apiV1Guard(dashboard, "settings", d.apiPairingsHandler))
	mux.HandleFunc("/api/v1/devices", d.apiV1Guard(dashboard, "settings", d.apiDevicesHandler))
	mux.HandleFunc("/api/v1/device", d.apiDeviceSelfHandler)
	mux.HandleFunc("/api/v1/device/push", d.apiDevicePushHandler)
	mux.HandleFunc("/api/v1/pair", d.apiPairHandler)
}

func (d *Daemon) apiV1Guard(dashboard *dashboardServer, scope string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cookieAuthorized := dashboard.authorized(r)
		bearerAuthorized := false
		if token := bearerToken(r); token != "" {
			if constantTokenEqual(token, dashboard.token) {
				bearerAuthorized = true
			} else if _, err := d.Registry.AuthenticateDevice(token, scope); err == nil {
				bearerAuthorized = true
				r = r.WithContext(context.WithValue(r.Context(), apiDeviceTokenContextKey{}, token))
			}
		}
		if !cookieAuthorized && !bearerAuthorized {
			writeAPIError(w, http.StatusUnauthorized, "unauthorized", "A valid Agenthail device token is required.")
			return
		}
		if cookieAuthorized && !bearerAuthorized && r.Method != http.MethodGet && r.Method != http.MethodHead && !sameOrigin(r) {
			writeAPIError(w, http.StatusForbidden, "cross_origin", "Cross-origin dashboard requests are not allowed.")
			return
		}
		next(w, r)
	}
}

func (d *Daemon) apiVersionHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	writeDashboardJSON(w, http.StatusOK, apiVersionResponse{Protocol: apiProtocolVersion, MinimumProtocol: apiProtocolVersion, MaximumProtocol: apiProtocolVersion, Authentication: "bearer", EventTransport: "sse", PushRelayURL: PushRelayURL()})
}

func (d *Daemon) apiEventsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeAPIError(w, http.StatusInternalServerError, "stream_unavailable", "Streaming is unavailable.")
		return
	}
	after := parseEventCursor(r)
	backlog, events, reset, cancel := d.events.subscribe(after)
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-transform")
	w.Header().Set("Connection", "keep-alive")
	if reset {
		writeSSEEvent(w, apiEvent{Type: "stream.reset", Timestamp: time.Now().UTC(), Data: json.RawMessage(`{"reason":"cursor_expired"}`)})
	} else {
		for _, event := range backlog {
			writeSSEEvent(w, event)
		}
	}
	flusher.Flush()
	keepalive := time.NewTicker(eventKeepalivePeriod)
	defer keepalive.Stop()
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
			writeSSEEvent(w, event)
			flusher.Flush()
		case <-keepalive.C:
			if !d.apiEventStreamAuthorized(r) {
				return
			}
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

func (d *Daemon) apiEventStreamAuthorized(r *http.Request) bool {
	token, _ := r.Context().Value(apiDeviceTokenContextKey{}).(string)
	if token == "" {
		return true
	}
	_, err := d.Registry.AuthenticateDevice(token, "read")
	return err == nil
}

func (d *Daemon) apiPairingsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to create a pairing.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Name   string   `json:"name"`
		Scopes []string `json:"scopes"`
	}
	if err := decodeAPIV1JSON(w, r, &request, true); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The pairing request is invalid.")
		return
	}
	endpoint, err := pairingEndpoint(r)
	if err != nil {
		writeAPIError(w, http.StatusConflict, "remote_access_required", err.Error())
		return
	}
	pairing, err := d.Registry.CreateDevicePairing(request.Name, request.Scopes, pairingLifetime)
	if err != nil {
		writeAPIError(w, http.StatusBadRequest, "pairing_failed", err.Error())
		return
	}
	values := url.Values{}
	values.Set("endpoint", endpoint)
	values.Set("secret", pairing.Secret)
	for _, scope := range pairing.Scopes {
		values.Add("scope", scope)
	}
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"id": pairing.ID, "expiresAt": pairing.ExpiresAt, "pairingURL": "agenthail://pair?" + values.Encode(), "endpoint": endpoint, "secret": pairing.Secret, "scopes": pairing.Scopes})
}

func (d *Daemon) apiPairHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to complete pairing.")
		return
	}
	defer r.Body.Close()
	var request struct {
		Secret string `json:"secret"`
		Name   string `json:"name"`
	}
	if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", "The pairing request is invalid.")
		return
	}
	device, token, err := d.Registry.CompleteDevicePairing(request.Secret, request.Name)
	if errors.Is(err, registry.ErrPairingExpired) {
		writeAPIError(w, http.StatusGone, "pairing_expired", err.Error())
		return
	}
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "pairing_invalid", err.Error())
		return
	}
	d.publishEvent("device.paired", device.ID, device)
	writeDashboardJSON(w, http.StatusCreated, map[string]any{"device": device, "token": token, "protocol": apiProtocolVersion})
}

func (d *Daemon) apiDevicesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		devices, err := d.Registry.ListDevices(r.URL.Query().Get("all") == "1")
		if err != nil {
			writeAPIError(w, http.StatusInternalServerError, "device_list_failed", err.Error())
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"devices": devices})
	case http.MethodDelete:
		defer r.Body.Close()
		var request struct {
			ID string `json:"id"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil || strings.TrimSpace(request.ID) == "" {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "A device id is required.")
			return
		}
		if err := d.Registry.RevokeDevice(request.ID); err != nil {
			writeAPIError(w, http.StatusNotFound, "device_not_found", err.Error())
			return
		}
		d.publishEvent("device.revoked", request.ID, map[string]string{"id": request.ID})
		writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or DELETE for this endpoint.")
	}
}

func (d *Daemon) apiDevicePushHandler(w http.ResponseWriter, r *http.Request) {
	device, err := d.Registry.AuthenticateDevice(bearerToken(r), "read")
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "device_unauthorized", "A paired device token is required.")
		return
	}
	switch r.Method {
	case http.MethodPut:
		defer r.Body.Close()
		var request struct {
			InstallationID string `json:"installationId"`
			Credential     string `json:"credential"`
		}
		if err := decodeAPIV1JSON(w, r, &request, false); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "The push registration is invalid.")
			return
		}
		if err := d.Registry.SaveDevicePushTarget(device.ID, request.InstallationID, request.Credential); err != nil {
			writeAPIError(w, http.StatusBadRequest, "push_registration_failed", err.Error())
			return
		}
		d.publishEvent("device.push.updated", device.ID, map[string]any{"enabled": true})
		writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
	case http.MethodDelete:
		if err := d.Registry.RemoveDevicePushTarget(device.ID); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "push_remove_failed", err.Error())
			return
		}
		d.publishEvent("device.push.updated", device.ID, map[string]any{"enabled": false})
		writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use PUT or DELETE for this endpoint.")
	}
}

func (d *Daemon) apiDeviceSelfHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use DELETE to revoke this device.")
		return
	}
	device, err := d.Registry.AuthenticateDevice(bearerToken(r), "read")
	if err != nil {
		writeAPIError(w, http.StatusUnauthorized, "device_unauthorized", "A paired device token is required.")
		return
	}
	if err := d.Registry.RevokeDevice(device.ID); err != nil {
		writeAPIError(w, http.StatusInternalServerError, "device_revoke_failed", err.Error())
		return
	}
	d.publishEvent("device.revoked", device.ID, map[string]string{"id": device.ID})
	writeDashboardJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func bearerToken(r *http.Request) string {
	value := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(value) < 8 || !strings.EqualFold(value[:7], "Bearer ") {
		return ""
	}
	return strings.TrimSpace(value[7:])
}

func constantTokenEqual(left, right string) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for i := range left {
		difference |= left[i] ^ right[i]
	}
	return difference == 0
}

func parseEventCursor(r *http.Request) uint64 {
	value := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if value == "" {
		value = strings.TrimSpace(r.URL.Query().Get("after"))
	}
	cursor, _ := strconv.ParseUint(value, 10, 64)
	return cursor
}

func writeSSEEvent(w io.Writer, event apiEvent) {
	payload, _ := json.Marshal(event)
	fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", event.ID, event.Type, payload)
}

func writeAPIError(w http.ResponseWriter, status int, code, message string) {
	writeDashboardJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func decodeAPIV1JSON(w http.ResponseWriter, r *http.Request, value any, allowEmpty bool) error {
	r.Body = http.MaxBytesReader(w, r.Body, apiRequestBodyLimit)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		if allowEmpty && errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func apiV1JSONHandler(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		adapter := &apiV1ResponseWriter{target: w, header: make(http.Header)}
		next(adapter, r)
		if adapter.passthrough {
			return
		}
		if adapter.status == 0 {
			adapter.WriteHeader(http.StatusOK)
			return
		}
		message := strings.TrimSpace(adapter.body.String())
		if adapter.truncated {
			message += " (response truncated)"
		}
		if message == "" {
			message = http.StatusText(adapter.status)
		}
		writeAPIError(w, adapter.status, apiV1StatusCode(adapter.status), message)
	}
}

type apiV1ResponseWriter struct {
	target      http.ResponseWriter
	header      http.Header
	status      int
	passthrough bool
	body        strings.Builder
	truncated   bool
}

func (w *apiV1ResponseWriter) Header() http.Header {
	return w.header
}

func (w *apiV1ResponseWriter) WriteHeader(status int) {
	if w.status != 0 {
		return
	}
	w.status = status
	if status < http.StatusBadRequest || strings.Contains(strings.ToLower(w.header.Get("Content-Type")), "application/json") {
		copyHTTPHeader(w.target.Header(), w.header)
		w.target.WriteHeader(status)
		w.passthrough = true
	}
}

func (w *apiV1ResponseWriter) Write(value []byte) (int, error) {
	written := len(value)
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	if w.passthrough {
		return w.target.Write(value)
	}
	remaining := apiErrorBodyLimit - w.body.Len()
	if remaining <= 0 {
		w.truncated = true
		return written, nil
	}
	if len(value) > remaining {
		value = value[:remaining]
		w.truncated = true
	}
	_, _ = w.body.Write(value)
	return written, nil
}

func copyHTTPHeader(target, source http.Header) {
	for key, values := range source {
		for _, value := range values {
			target.Add(key, value)
		}
	}
}

func apiV1StatusCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request"
	case http.StatusUnauthorized:
		return "unauthorized"
	case http.StatusForbidden:
		return "forbidden"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusMethodNotAllowed:
		return "method_not_allowed"
	case http.StatusConflict:
		return "conflict"
	case http.StatusServiceUnavailable:
		return "service_unavailable"
	default:
		return "request_failed"
	}
}

func requestEndpoint(r *http.Request) string {
	scheme := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto"))
	if scheme == "" {
		if r.TLS != nil {
			scheme = "https"
		} else {
			scheme = "http"
		}
	}
	host := strings.TrimSpace(r.Header.Get("X-Forwarded-Host"))
	if host == "" {
		host = r.Host
	}
	return scheme + "://" + host
}

func pairingEndpoint(r *http.Request) (string, error) {
	config, err := LoadDashboardConfig()
	if err != nil {
		return "", err
	}
	status := RemoteAccessStatusForConfig(config)
	if !status.Enabled || status.URL == "" {
		message := "Enable private Tailscale access in Operations before pairing an iPhone."
		if status.Error != "" {
			message += " " + status.Error
		}
		return "", errors.New(message)
	}
	remote, err := url.Parse(status.URL)
	if err != nil || remote.Scheme != "https" || remote.Host == "" {
		return "", fmt.Errorf("the Tailscale pairing address is invalid")
	}
	incoming, err := url.Parse(requestEndpoint(r))
	if err != nil {
		return "", fmt.Errorf("parse pairing endpoint: %w", err)
	}
	incomingIP := net.ParseIP(incoming.Hostname())
	incomingIsLoopback := incoming.Hostname() == "localhost" || incomingIP != nil && incomingIP.IsLoopback()
	if !incomingIsLoopback && (incoming.Scheme != "https" || !strings.EqualFold(incoming.Host, remote.Host)) {
		return "", fmt.Errorf("pairing is only available through the configured Tailscale address")
	}
	remote.Path = ""
	remote.RawPath = ""
	remote.RawQuery = ""
	remote.Fragment = ""
	return strings.TrimRight(remote.String(), "/"), nil
}

package daemon

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAPIV1PairsAuthenticatesAndRevokesDevice(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	tailscale := filepath.Join(t.TempDir(), "tailscale")
	script := `#!/bin/sh
case "$*" in
  "status --json") printf '%s\n' '{"BackendState":"Running","Self":{"DNSName":"agenthail.example.ts.net.","Online":true}}' ;;
  "serve status --json") printf '%s\n' '{"AllowFunnel":{},"Web":{"agenthail.example.ts.net:7412":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:7412"}}}}}' ;;
  "serve status") printf '%s\n' 'https://agenthail.example.ts.net:7412 (tailnet only)' ;;
esac
`
	if err := os.WriteFile(tailscale, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := SaveDashboardConfig(DashboardConfig{Enabled: true, Listen: defaultDashboardListen, RemoteAccess: RemoteAccessConfig{Enabled: true, Provider: "tailscale", Port: 7412, TailscalePath: tailscale}}); err != nil {
		t.Fatal(err)
	}
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "bootstrap-secret"})
	pairRequest := httptest.NewRequest(http.MethodPost, "/api/v1/pairings", strings.NewReader(`{"name":"Phone","scopes":["read","control"]}`))
	pairRequest.Header.Set("Authorization", "Bearer bootstrap-secret")
	pairRequest.Host = "agenthail.example.ts.net:7412"
	pairRequest.Header.Set("X-Forwarded-Proto", "https")
	pairResponse := httptest.NewRecorder()
	handler.ServeHTTP(pairResponse, pairRequest)
	if pairResponse.Code != http.StatusCreated {
		t.Fatalf("create pairing status=%d body=%s", pairResponse.Code, pairResponse.Body.String())
	}
	var pairing struct {
		Secret     string `json:"secret"`
		PairingURL string `json:"pairingURL"`
	}
	if err := json.Unmarshal(pairResponse.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	if pairing.Secret == "" || !strings.HasPrefix(pairing.PairingURL, "agenthail://pair?") || !strings.Contains(pairing.PairingURL, "agenthail.example.ts.net") {
		t.Fatalf("pairing=%+v", pairing)
	}
	completeBody, _ := json.Marshal(map[string]string{"secret": pairing.Secret, "name": "My iPhone"})
	completeResponse := httptest.NewRecorder()
	handler.ServeHTTP(completeResponse, httptest.NewRequest(http.MethodPost, "/api/v1/pair", bytes.NewReader(completeBody)))
	if completeResponse.Code != http.StatusCreated {
		t.Fatalf("complete pairing status=%d body=%s", completeResponse.Code, completeResponse.Body.String())
	}
	var completed struct {
		Token  string `json:"token"`
		Device struct {
			ID string `json:"id"`
		} `json:"device"`
	}
	if err := json.Unmarshal(completeResponse.Body.Bytes(), &completed); err != nil {
		t.Fatal(err)
	}
	versionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	versionRequest.Header.Set("Authorization", "Bearer "+completed.Token)
	versionResponse := httptest.NewRecorder()
	handler.ServeHTTP(versionResponse, versionRequest)
	if versionResponse.Code != http.StatusOK || !strings.Contains(versionResponse.Body.String(), `"protocol":1`) {
		t.Fatalf("version status=%d body=%s", versionResponse.Code, versionResponse.Body.String())
	}
	pushBody, _ := json.Marshal(map[string]string{"installationId": "installation", "credential": "credential"})
	pushRequest := httptest.NewRequest(http.MethodPut, "/api/v1/device/push", bytes.NewReader(pushBody))
	pushRequest.Header.Set("Authorization", "Bearer "+completed.Token)
	pushResponse := httptest.NewRecorder()
	handler.ServeHTTP(pushResponse, pushRequest)
	if pushResponse.Code != http.StatusOK {
		t.Fatalf("push registration status=%d body=%s", pushResponse.Code, pushResponse.Body.String())
	}
	devicesRequest := httptest.NewRequest(http.MethodGet, "/api/v1/devices", nil)
	devicesRequest.Header.Set("Authorization", "Bearer bootstrap-secret")
	devicesResponse := httptest.NewRecorder()
	handler.ServeHTTP(devicesResponse, devicesRequest)
	if devicesResponse.Code != http.StatusOK || !strings.Contains(devicesResponse.Body.String(), `"pushEnabled":true`) || strings.Contains(devicesResponse.Body.String(), "credential") {
		t.Fatalf("devices status=%d body=%s", devicesResponse.Code, devicesResponse.Body.String())
	}
	revokeBody, _ := json.Marshal(map[string]string{"id": completed.Device.ID})
	revokeRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/devices", bytes.NewReader(revokeBody))
	revokeRequest.Header.Set("Authorization", "Bearer bootstrap-secret")
	revokeResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeResponse.Code, revokeResponse.Body.String())
	}
	deniedRequest := httptest.NewRequest(http.MethodGet, "/api/v1/version", nil)
	deniedRequest.Header.Set("Authorization", "Bearer "+completed.Token)
	deniedResponse := httptest.NewRecorder()
	handler.ServeHTTP(deniedResponse, deniedRequest)
	if deniedResponse.Code != http.StatusUnauthorized || !strings.Contains(deniedResponse.Body.String(), `"code":"unauthorized"`) {
		t.Fatalf("denied status=%d body=%s", deniedResponse.Code, deniedResponse.Body.String())
	}
}

func TestEventHubReplaysAndDisconnectsSlowSubscribers(t *testing.T) {
	hub := newEventHub(nil)
	first, err := hub.publish("session.updated", "one", map[string]string{"status": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := hub.publish("turn.completed", "one", map[string]string{"turnId": "two"})
	if err != nil {
		t.Fatal(err)
	}
	initial, initialEvents, initialReset, initialCancel := hub.subscribe(0)
	if initialReset || len(initial) != 2 || initial[0].ID != first.ID || initial[1].ID != second.ID {
		t.Fatalf("initial reset=%v backlog=%+v", initialReset, initial)
	}
	initialCancel()
	for range initialEvents {
	}
	backlog, events, reset, cancel := hub.subscribe(first.ID)
	defer cancel()
	if reset || len(backlog) != 1 || backlog[0].ID != second.ID {
		t.Fatalf("reset=%v backlog=%+v", reset, backlog)
	}
	for index := 0; index < cap(events)+1; index++ {
		if _, err := hub.publish("state.changed", "", index); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case _, open := <-events:
		if open {
			for range events {
			}
		}
	case <-time.After(time.Second):
		t.Fatal("slow subscriber remained connected")
	}
}

func TestAPIV1EventStreamResumesFromLastEventID(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	first, err := d.events.publish("session.updated", "one", map[string]string{"status": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.events.publish("turn.completed", "one", map[string]string{"turnId": "done"})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: "secret"}))
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("Last-Event-ID", strconv.FormatUint(first.ID, 10))
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader := bufio.NewReader(response.Body)
	var body strings.Builder
	for !strings.Contains(body.String(), "\n\n") {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		body.WriteString(line)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(body.String(), "id: "+strconv.FormatUint(second.ID, 10)) || !strings.Contains(body.String(), "event: turn.completed") {
		t.Fatalf("status=%d event=%q", response.StatusCode, body.String())
	}
}

func TestAPIV1RevocationClosesOpenDeviceEventStream(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	device, token, err := d.Registry.CompleteDevicePairing(mustCreatePairing(t, d), "Phone")
	if err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	server := httptest.NewServer(handler)
	defer server.Close()
	request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	revokeBody, _ := json.Marshal(map[string]string{"id": device.ID})
	revokeRequest := httptest.NewRequest(http.MethodDelete, "/api/v1/devices", bytes.NewReader(revokeBody))
	revokeRequest.Header.Set("Authorization", "Bearer secret")
	revokeResponse := httptest.NewRecorder()
	handler.ServeHTTP(revokeResponse, revokeRequest)
	if revokeResponse.Code != http.StatusOK {
		t.Fatalf("revoke status=%d body=%s", revokeResponse.Code, revokeResponse.Body.String())
	}
	read := make(chan []byte, 1)
	go func() {
		body, _ := io.ReadAll(response.Body)
		read <- body
	}()
	select {
	case body := <-read:
		if strings.Contains(string(body), "device.revoked") {
			t.Fatalf("revoked stream received event: %q", body)
		}
	case <-time.After(time.Second):
		t.Fatal("revoked device stream remained open")
	}
}

func TestAPIV1SnapshotCursorClosesBootstrapGap(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	first, err := d.events.publish("session.updated", "one", map[string]string{"status": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot struct {
		EventCursor uint64 `json:"eventCursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.EventCursor != first.ID {
		t.Fatalf("snapshot cursor=%d want=%d", snapshot.EventCursor, first.ID)
	}
	second, err := d.events.publish("turn.completed", "one", map[string]string{"turnId": "gap"})
	if err != nil {
		t.Fatal(err)
	}
	backlog, _, reset, cancel := d.events.subscribe(snapshot.EventCursor)
	defer cancel()
	if reset || len(backlog) != 1 || backlog[0].ID != second.ID {
		t.Fatalf("reset=%v backlog=%+v", reset, backlog)
	}
}

func TestAPIV1ZeroSnapshotCursorReplaysFirstEvent(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("snapshot status=%d body=%s", response.Code, response.Body.String())
	}
	var snapshot struct {
		EventCursor uint64 `json:"eventCursor"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.EventCursor != 0 {
		t.Fatalf("snapshot cursor=%d want=0", snapshot.EventCursor)
	}
	first, err := d.events.publish("turn.completed", "one", map[string]string{"turnId": "first"})
	if err != nil {
		t.Fatal(err)
	}
	backlog, _, reset, cancel := d.events.subscribe(snapshot.EventCursor)
	defer cancel()
	if reset || len(backlog) != 1 || backlog[0].ID != first.ID {
		t.Fatalf("reset=%v backlog=%+v", reset, backlog)
	}
}

func TestEventHubReplaysAcrossDaemonRestart(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	first, err := d.events.publish("session.updated", "one", map[string]string{"status": "busy"})
	if err != nil {
		t.Fatal(err)
	}
	restarted := newEventHub(reg)
	second, err := restarted.publish("turn.completed", "one", map[string]string{"turnId": "done"})
	if err != nil {
		t.Fatal(err)
	}
	if second.ID <= first.ID {
		t.Fatalf("event ids did not survive restart: first=%d second=%d", first.ID, second.ID)
	}
	backlog, _, reset, cancel := restarted.subscribe(first.ID)
	defer cancel()
	if reset || len(backlog) != 1 || backlog[0].ID != second.ID {
		t.Fatalf("reset=%v backlog=%+v", reset, backlog)
	}
}

func TestAPIV1LegacyHandlerErrorsUseTypedJSON(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodGet, "/api/v1/session", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code < 400 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("content-type=%q body=%s", contentType, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code == "" || envelope.Error.Message == "" {
		t.Fatalf("envelope=%+v", envelope)
	}
}

func TestAPIV1JSONHandlerStreamsSuccessfulResponses(t *testing.T) {
	called := false
	handler := apiV1JSONHandler(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	response := httptest.NewRecorder()
	handler(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if !called || response.Code != http.StatusCreated || response.Body.String() != `{"ok":true}` {
		t.Fatalf("called=%v status=%d body=%q", called, response.Code, response.Body.String())
	}
}

func TestAPIV1RoutesRejectUnauthorizedRequestsWithTypedJSON(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	cases := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/version"},
		{http.MethodGet, "/api/v1/snapshot"},
		{http.MethodGet, "/api/v1/events"},
		{http.MethodGet, "/api/v1/session?id=missing"},
		{http.MethodGet, "/api/v1/models?surface=missing"},
		{http.MethodGet, "/api/v1/history"},
		{http.MethodPost, "/api/v1/actions"},
		{http.MethodGet, "/api/v1/settings"},
		{http.MethodPost, "/api/v1/pairings"},
		{http.MethodGet, "/api/v1/devices"},
	}
	for _, test := range cases {
		t.Run(test.method+" "+test.path, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(test.method, test.path, nil))
			assertAPIV1Error(t, response, http.StatusUnauthorized, "unauthorized")
		})
	}
}

func TestAPIV1RoutesRejectUnsupportedMethodsWithTypedJSON(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	paths := []string{
		"/api/v1/version",
		"/api/v1/snapshot",
		"/api/v1/events",
		"/api/v1/session",
		"/api/v1/models",
		"/api/v1/history",
		"/api/v1/actions",
		"/api/v1/settings",
		"/api/v1/pairings",
		"/api/v1/devices",
		"/api/v1/device",
		"/api/v1/device/push",
		"/api/v1/pair",
	}
	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, path, nil)
			request.Header.Set("Authorization", "Bearer secret")
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if path == "/api/v1/device/push" {
				assertAPIV1Error(t, response, http.StatusUnauthorized, "device_unauthorized")
				return
			}
			assertAPIV1Error(t, response, http.StatusMethodNotAllowed, "method_not_allowed")
		})
	}
}

func TestAPIV1MutationFailuresUseTypedJSON(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	cases := []struct {
		name   string
		method string
		path   string
		body   string
		status int
		code   string
		token  string
	}{
		{"action", http.MethodPost, "/api/v1/actions", `{"action":`, http.StatusBadRequest, "invalid_request", "secret"},
		{"settings", http.MethodPost, "/api/v1/settings", `{"action":`, http.StatusBadRequest, "invalid_request", "secret"},
		{"pairing", http.MethodPost, "/api/v1/pairings", `{`, http.StatusBadRequest, "invalid_request", "secret"},
		{"device", http.MethodDelete, "/api/v1/devices", `{}`, http.StatusBadRequest, "invalid_request", "secret"},
		{"complete pairing", http.MethodPost, "/api/v1/pair", `{`, http.StatusBadRequest, "invalid_request", ""},
		{"self revoke", http.MethodDelete, "/api/v1/device", ``, http.StatusUnauthorized, "device_unauthorized", "invalid"},
		{"push", http.MethodPut, "/api/v1/device/push", `{`, http.StatusUnauthorized, "device_unauthorized", "invalid"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			if test.token != "" {
				request.Header.Set("Authorization", "Bearer "+test.token)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			assertAPIV1Error(t, response, test.status, test.code)
		})
	}
}

func TestAPIV1RejectsOversizedAndTrailingJSONBodies(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	cases := []struct {
		name string
		body string
	}{
		{"oversized", `{"secret":"` + strings.Repeat("x", apiRequestBodyLimit) + `"}`},
		{"trailing", `{"secret":"missing"} {}`},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/pair", strings.NewReader(test.body)))
			assertAPIV1Error(t, response, http.StatusBadRequest, "invalid_request")
		})
	}
}

func assertAPIV1Error(t *testing.T, response *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status=%d want=%d body=%s", response.Code, status, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); !strings.Contains(contentType, "application/json") {
		t.Fatalf("content-type=%q body=%s", contentType, response.Body.String())
	}
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Error.Code != code || strings.TrimSpace(envelope.Error.Message) == "" {
		t.Fatalf("error=%+v want code=%q", envelope.Error, code)
	}
}

func TestAPIV1RejectsMutationWithoutPublishingChange(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	before := len(d.events.history)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", strings.NewReader(`{"action":`))
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if len(d.events.history) != before {
		t.Fatalf("rejected mutation published %d event(s)", len(d.events.history)-before)
	}
}

func TestAPIV1DeviceCanRevokeItself(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	device, token, err := d.Registry.CompleteDevicePairing(mustCreatePairing(t, d), "Phone")
	if err != nil {
		t.Fatal(err)
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	request := httptest.NewRequest(http.MethodDelete, "/api/v1/device", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if _, err := d.Registry.AuthenticateDevice(token, "read"); err == nil {
		t.Fatalf("device %s remained authorized", device.ID)
	}
}

func TestAPIV1ManagesDesktopNotifications(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	helper := filepath.Join(t.TempDir(), "Agenthail")
	script := `#!/bin/sh
case "$1" in
  status|request) printf '%s\n' '{"available":true,"authorization":"authorized","authorized":true,"alerts":true,"sounds":true}' ;;
  send|settings) exit 0 ;;
  *) exit 64 ;;
esac
`
	if err := os.WriteFile(helper, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AGENTHAIL_MAC_APP", helper)
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	settingsRequest := httptest.NewRequest(http.MethodGet, "/api/v1/settings", nil)
	settingsRequest.Header.Set("Authorization", "Bearer secret")
	settingsResponse := httptest.NewRecorder()
	handler.ServeHTTP(settingsResponse, settingsRequest)
	if settingsResponse.Code != http.StatusOK || !strings.Contains(settingsResponse.Body.String(), `"notifications"`) {
		t.Fatalf("settings status=%d body=%s", settingsResponse.Code, settingsResponse.Body.String())
	}
	for _, action := range []string{"notifications-enable", "notifications-test", "notifications-disable", "notifications-settings"} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/settings", strings.NewReader(`{"action":"`+action+`"}`))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", action, response.Code, response.Body.String())
		}
	}
}

func mustCreatePairing(t *testing.T, d *Daemon) string {
	t.Helper()
	pairing, err := d.Registry.CreateDevicePairing("Phone", []string{"read", "control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	return pairing.Secret
}

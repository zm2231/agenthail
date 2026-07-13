package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	registrypkg "github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestDashboardListenIsLoopbackOnly(t *testing.T) {
	for _, listen := range []string{"127.0.0.1:7412", "[::1]:7412", "localhost:7412"} {
		if err := validateDashboardListen(listen); err != nil {
			t.Fatalf("%s: %v", listen, err)
		}
	}
	if err := validateDashboardListen("0.0.0.0:7412"); err == nil || !strings.Contains(err.Error(), "Tailscale Serve") {
		t.Fatalf("err=%v", err)
	}
}

func TestDashboardRequiresTokenAndRejectsCrossOriginActions(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/state", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("state status=%d", unauthorized.Code)
	}

	bootstrap := httptest.NewRecorder()
	handler.ServeHTTP(bootstrap, httptest.NewRequest(http.MethodGet, "/?token=secret", nil))
	if bootstrap.Code != http.StatusFound || len(bootstrap.Result().Cookies()) != 1 {
		t.Fatalf("bootstrap status=%d cookies=%d", bootstrap.Code, len(bootstrap.Result().Cookies()))
	}
	cookie := bootstrap.Result().Cookies()[0]
	if cookie.MaxAge != dashboardCookieMaxAge {
		t.Fatalf("dashboard cookie max age=%d, want %d", cookie.MaxAge, dashboardCookieMaxAge)
	}
	state := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	request.AddCookie(cookie)
	handler.ServeHTTP(state, request)
	if state.Code != http.StatusOK || !strings.Contains(state.Body.String(), `"sessions"`) {
		t.Fatalf("state=%d body=%s", state.Code, state.Body.String())
	}

	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": "from", "message": "hello"})
	mutating := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	mutating.Header.Set("Origin", "https://attacker.example")
	mutating.AddCookie(cookie)
	blocked := httptest.NewRecorder()
	handler.ServeHTTP(blocked, mutating)
	if blocked.Code != http.StatusForbidden {
		t.Fatalf("cross-origin action status=%d", blocked.Code)
	}

	tailscale := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	tailscale.Header.Set("Origin", "https://agenthail.tailnet.ts.net")
	tailscale.Host = "agenthail.tailnet.ts.net"
	tailscale.AddCookie(cookie)
	allowed := httptest.NewRecorder()
	handler.ServeHTTP(allowed, tailscale)
	if allowed.Code != http.StatusOK {
		t.Fatalf("tailscale action status=%d body=%s", allowed.Code, allowed.Body.String())
	}
}

func TestDashboardActionSendsToRegisteredSession(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	body, _ := json.Marshal(map[string]string{"action": "send", "sessionId": "from", "message": "ship this"})
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewReader(body))
	request.Header.Set("Origin", "http://example.test")
	request.Host = "example.test"
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || len(fake.sent) != 1 || fake.sent[0] != "ship this" {
		t.Fatalf("status=%d sent=%v body=%s", response.Code, fake.sent, response.Body.String())
	}
}

func TestDashboardCapabilitiesMakeUnloadedCodexReadOnly(t *testing.T) {
	capabilities, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded")}, surface.Capabilities{Send: true, Model: true})
	if !readOnly || reason == "" || capabilities.Send || capabilities.Model {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", capabilities, readOnly, reason)
	}
}

func TestDashboardCapabilitiesKeepUnloadedDesktopThreadWritable(t *testing.T) {
	capabilities := surface.Capabilities{Send: true, Model: true}
	got, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), Source: "vscode", Transport: "desktop"}, capabilities)
	if readOnly || reason != "" || !got.Send || !got.Model {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", got, readOnly, reason)
	}
}

func TestDashboardCapabilitiesMakePlainCodexTerminalReadOnly(t *testing.T) {
	capabilities, readOnly, reason := dashboardCapabilities(surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, Source: "cli", Transport: "readOnly"}, surface.Capabilities{Send: true, Steer: true, Compact: true})
	if !readOnly || reason == "" || capabilities.Send || capabilities.Steer || capabilities.Compact {
		t.Fatalf("capabilities=%+v readOnly=%v reason=%q", capabilities, readOnly, reason)
	}
}

func TestDashboardRejectsReadOnlyCodexRoutingDestination(t *testing.T) {
	daemon, registry, _, _, target := daemonFixture(t)
	target.Source = "cli"
	target.Transport = "readOnly"
	if err := registry.RegisterSession(target); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.CreateChannel("reviewers"); err != nil {
		t.Fatal(err)
	}
	queueID, err := registry.QueueMessageWithOptions(target.ID, "do not retry", "", surface.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := registry.ClaimNextMessage(target.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := registry.NackMessage(queueID, fmt.Errorf("read only"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"action":"channel-add","channel":"reviewers","targetId":"to"}`,
		`{"action":"relay-add","fromId":"from","toId":"to","pattern":".*"}`,
		fmt.Sprintf(`{"action":"queue-retry","queueId":%d}`, queueID),
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(body))
		daemon.dashboardActionHandler(response, request)
		if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "read only") {
			t.Fatalf("body=%s code=%d", response.Body.String(), response.Code)
		}
	}
	item, err := registry.QueueItem(queueID)
	if err != nil || item.Status != "dead" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	members, err := registry.ChannelMembers("reviewers")
	if err != nil || len(members) != 0 {
		t.Fatalf("members=%v err=%v", members, err)
	}
	routes, err := registry.ListRoutes()
	if err != nil || len(routes) != 0 {
		t.Fatalf("routes=%v err=%v", routes, err)
	}
	if err := registry.AddToChannel("reviewers", target.ID); err != nil {
		t.Fatal(err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/action", strings.NewReader(`{"action":"channel-send","channel":"reviewers","message":"handoff"}`))
	daemon.dashboardActionHandler(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "1 failed") {
		t.Fatalf("body=%s code=%d", response.Body.String(), response.Code)
	}
}

func TestDashboardStateCachesSurfaceDiscovery(t *testing.T) {
	d, _, fake, _, _ := daemonFixture(t)
	dashboard := &dashboardServer{token: "secret"}
	handler := d.dashboardHandler(dashboard)
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/api/state", nil)
		req.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("state request %d status=%d body=%s", i, res.Code, res.Body.String())
		}
	}
	if got := fake.listCalls.Load(); got != 1 {
		t.Fatalf("surface list called %d times, want one cached discovery", got)
	}
}

func TestDashboardHistoryIsAuthorizedAndPaginated(t *testing.T) {
	d, registry, _, _, _ := daemonFixture(t)
	for i := 0; i < 4; i++ {
		if err := registry.RecordHistory(registrypkg.HistoryEntry{Kind: "sent", Message: fmt.Sprintf("message %d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	unauthorized := httptest.NewRecorder()
	handler.ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/api/history?limit=2", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status=%d", unauthorized.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/history?limit=2", nil)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var first struct {
		Items      []dashboardHistory `json:"items"`
		HasMore    bool               `json:"hasMore"`
		NextBefore int64              `json:"nextBefore"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 2 || !first.HasMore || first.NextBefore != first.Items[1].ID {
		t.Fatalf("first page=%+v", first)
	}
	nextRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/history?limit=2&before=%d", first.NextBefore), nil)
	nextRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	nextResponse := httptest.NewRecorder()
	handler.ServeHTTP(nextResponse, nextRequest)
	if nextResponse.Code != http.StatusOK || strings.Contains(nextResponse.Body.String(), fmt.Sprintf(`"id":%d`, first.Items[1].ID)) {
		t.Fatalf("second page status=%d body=%s", nextResponse.Code, nextResponse.Body.String())
	}
	filteredRequest := httptest.NewRequest(http.MethodGet, "/api/history?limit=25&q=message+3&kind=sent", nil)
	filteredRequest.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "secret"})
	filteredResponse := httptest.NewRecorder()
	handler.ServeHTTP(filteredResponse, filteredRequest)
	if filteredResponse.Code != http.StatusOK || !strings.Contains(filteredResponse.Body.String(), "message 3") || strings.Contains(filteredResponse.Body.String(), "message 2") {
		t.Fatalf("filtered status=%d body=%s", filteredResponse.Code, filteredResponse.Body.String())
	}
}

func TestDashboardStatePrefersLiveSurfaceStatus(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, registry, fake, from, _ := daemonFixture(t)
	if err := registry.SaveRuntimeState(from.ID, surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	from.Status = surface.StatusBusy
	from.LastActive = time.Now()
	fake.sessions[from.ID] = from

	state, err := d.dashboardState(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, session := range state.Sessions {
		if session.ID == from.ID {
			if session.Status != surface.StatusBusy {
				t.Fatalf("dashboard status=%s, want live status %s", session.Status, surface.StatusBusy)
			}
			return
		}
	}
	t.Fatal("live session missing from dashboard state")
}

func TestDashboardSessionPresenceUsesSurfaceSpecificTruth(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	tests := []struct {
		name        string
		session     surface.Session
		queueCount  int
		open        bool
		wantCurrent bool
		wantReason  string
	}{
		{name: "open idle Claude", session: surface.Session{Surface: surface.KindClaude, Status: surface.StatusIdle}, open: true, wantCurrent: true, wantReason: "open"},
		{name: "closed busy Claude is stale", session: surface.Session{Surface: surface.KindClaude, Status: surface.StatusBusy}, wantCurrent: false},
		{name: "queued closed Claude", session: surface.Session{Surface: surface.KindClaude}, queueCount: 1, wantCurrent: true, wantReason: "queued"},
		{name: "recent Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: now.Add(-4 * time.Hour)}, wantCurrent: true, wantReason: "recent"},
		{name: "old Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusIdle, LastActive: now.Add(-6 * time.Hour)}, wantCurrent: false},
		{name: "historical Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.SessionStatus("notLoaded"), LastActive: now.Add(-time.Hour)}, wantCurrent: false},
		{name: "busy Codex", session: surface.Session{Surface: surface.KindCodex, Status: surface.StatusBusy, LastActive: now.Add(-48 * time.Hour)}, wantCurrent: true, wantReason: "working"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			current, reason := dashboardSessionPresence(test.session, test.queueCount, test.open, 5, now)
			if current != test.wantCurrent || reason != test.wantReason {
				t.Fatalf("current=%v reason=%q, want current=%v reason=%q", current, reason, test.wantCurrent, test.wantReason)
			}
		})
	}
}

func TestClaudeProcessOpenRejectsOtherProcesses(t *testing.T) {
	if claudeProcessOpen(context.Background(), os.Getpid()) {
		t.Fatal("non-Claude process was recognized as an open Claude session")
	}
	if claudeProcessOpen(context.Background(), 0) {
		t.Fatal("zero PID was recognized as alive")
	}
}

package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

func TestDashboardStatePrefersLiveSurfaceStatus(t *testing.T) {
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

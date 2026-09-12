package daemon

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestVoiceEndpointsRequirePairedControlAndNativeBearer(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	h := d.dashboardHandler(&dashboardServer{token: "fixture-token"})
	for _, path := range []string{"/api/v1/voice", "/api/v1/voice/peer"} {
		for _, bearer := range []string{"", "invalid-device-token"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "fixture-token"})
			if bearer != "" {
				request.Header.Set("Authorization", "Bearer "+bearer)
			}
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("dashboard cookie bypassed native device authentication on %s: %d", path, response.Code)
			}
		}
		r := httptest.NewRequest(http.MethodGet, path, nil)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", path, w.Code)
		}
		r = httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Authorization", "Bearer fixture-token")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 200 {
			t.Fatalf("authenticated %s: %d %s", path, w.Code, w.Body.String())
		}
		if path == "/api/v1/voice/peer" {
			if !strings.Contains(w.Header().Get("Content-Security-Policy"), "script-src 'sha256-") {
				t.Fatal("media page has no executable script allowlist")
			}
			if strings.Contains(w.Body.String(), "fixture-token") {
				t.Fatal("paired token exposed to media page")
			}
		}
	}
}

func TestVoiceControlScopeAndRevocationApplyToEveryEndpoint(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	h := d.dashboardHandler(&dashboardServer{token: "bootstrap"})
	for _, scopes := range [][]string{{"read"}, {"read", "control"}} {
		pairing, err := d.Registry.CreateDevicePairing("fixture phone", scopes, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		device, token, err := d.Registry.CompleteDevicePairing(pairing.Secret, "")
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/v1/voice", "/api/v1/voice/peer"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			want := http.StatusOK
			if len(scopes) == 1 {
				want = http.StatusUnauthorized
			}
			if response.Code != want {
				t.Fatalf("scopes=%v path=%s status=%d body=%s", scopes, path, response.Code, response.Body.String())
			}
		}
		if err := d.Registry.RevokeDevice(device.ID); err != nil {
			t.Fatal(err)
		}
		for _, path := range []string{"/api/v1/voice", "/api/v1/voice/peer"} {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			request.Header.Set("Authorization", "Bearer "+token)
			response := httptest.NewRecorder()
			h.ServeHTTP(response, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("revoked %s: %d", path, response.Code)
			}
		}
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/voice"
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

type ownerFixtureProvider struct{}

func (ownerFixtureProvider) Create(context.Context, string, string) (*surface.Session, error) {
	return &surface.Session{ID: "fixture-operator", Surface: surface.KindCodex, Name: "fixture operator"}, nil
}
func (ownerFixtureProvider) Cursor(context.Context) (voice.Cursor, error) {
	return voice.Cursor{Source: "fixture"}, nil
}
func (ownerFixtureProvider) Poll(_ context.Context, after voice.Cursor, _ string) (voice.Batch, error) {
	return voice.Batch{Cursor: after}, nil
}
func (ownerFixtureProvider) Request(context.Context, *surface.Session, string, map[string]any) error {
	return nil
}
func (ownerFixtureProvider) Interrupt(context.Context, *surface.Session) error { return nil }

// The web dashboard and the Mac app both hold the dashboard token, but the web
// route and the v1 route derive different owners from it, so neither can take
// over the other's call.
func TestVoiceDashboardAndMacAppAreSeparateCallers(t *testing.T) {
	s := voice.New(filepath.Join(t.TempDir(), "voice", "operator.json"), ownerFixtureProvider{}, nil, "agenthail")
	t.Cleanup(s.Close)
	web := dashboardVoiceHandler(s, "fixture-token")
	mac := voiceHandler(s)
	call := func(h http.HandlerFunc, method, body string) (int, voice.State) {
		t.Helper()
		r := httptest.NewRequest(method, "/api/v1/voice", strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer fixture-token")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h(w, r)
		var state voice.State
		if w.Code == http.StatusOK {
			if err := json.Unmarshal(w.Body.Bytes(), &state); err != nil {
				t.Fatal(err)
			}
		}
		return w.Code, state
	}
	if code, _ := call(web, http.MethodPost, `{"action":"prepare"}`); code != http.StatusOK {
		t.Fatalf("web prepare: %d", code)
	}
	if code, state := call(web, http.MethodPost, `{"action":"start","attemptId":"web-attempt","sdp":"v=0 web"}`); code != http.StatusOK || state.Phase != "starting" {
		t.Fatalf("web start: %d %+v", code, state)
	}
	if _, state := call(mac, http.MethodGet, ""); !state.Occupied {
		t.Fatalf("the Mac app sees the web call as its own: %+v", state)
	}
	if _, state := call(web, http.MethodGet, ""); state.Occupied {
		t.Fatal("the web dashboard lost ownership of its own call")
	}
	if code, _ := call(mac, http.MethodPost, `{"action":"stop","attemptId":"web-attempt"}`); code != http.StatusConflict {
		t.Fatalf("the Mac app stopped the web call: %d", code)
	}
}

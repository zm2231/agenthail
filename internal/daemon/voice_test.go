package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestVoiceHostToolsReadCatalogAndSharedJournal(t *testing.T) {
	d, reg, base, from, _ := daemonFixture(t)
	from.Name, from.Transcript = "Selected worker", "/private/test/transcript"
	if err := reg.RegisterSession(from); err != nil {
		t.Fatal(err)
	}
	base.sessions[from.ID] = from
	d.discoverCatalog(context.Background())
	adapter := &sourceCountingSurface{
		daemonSurface: base,
		started:       make(chan struct{}, 1),
		events:        make(chan surface.StreamEvent, 1),
		items:         []surface.TimelineItem{{ID: "answer", Kind: "text", Role: "assistant", Text: "Current worker result"}},
	}
	adapter.caps.Stream = true
	d.Surfaces = []surface.Surface{adapter}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	provider := daemonVoiceStreamProvider{daemon: d}
	rows, err := provider.ListVoiceSessions(ctx, "SELECTED")
	if err != nil || len(rows) != 1 || rows[0].ID != from.ID || rows[0].Transcript != "" {
		t.Fatalf("catalog=%+v err=%v", rows, err)
	}
	items, err := provider.ReadVoiceSession(ctx, &from)
	if err != nil || len(items) != 1 || items[0].ItemID != "answer" || items[0].Body != "Current worker result" {
		t.Fatalf("journal=%+v err=%v", items, err)
	}
}

func TestVoiceSessionListExcludesRemovedBusyHistory(t *testing.T) {
	d, reg, adapter, from, _ := daemonFixture(t)
	from.Status = surface.StatusBusy
	adapter.sessions[from.ID] = from
	d.discoverCatalog(context.Background())
	delete(adapter.sessions, from.ID)
	d.discoverCatalog(context.Background())
	d.discoverCatalog(context.Background())
	stored, err := reg.Session(from.ID)
	if err != nil || stored.Status != surface.StatusBusy {
		t.Fatalf("historical fixture=%+v err=%v", stored, err)
	}
	rows, err := (daemonVoiceStreamProvider{daemon: d}).ListVoiceSessions(context.Background(), from.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("removed historical session returned: %+v err=%v", rows, err)
	}
}

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

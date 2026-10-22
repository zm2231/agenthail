package daemon

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func pairingRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Authorization", "Bearer bootstrap-secret")
	request.Host = "agenthail.example.ts.net:7412"
	request.Header.Set("X-Forwarded-Proto", "https")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestAPIV1PairingCodeReportsTheDeviceThatUsedItAndNamesThisHost(t *testing.T) {
	pairingRemoteAccess(t)
	d, _, _, _, _ := daemonFixture(t)
	d.setHostIdentity(registry.HostIdentity{ID: "h0123456789ab", Name: "server"})
	handler := d.dashboardHandler(&dashboardServer{token: "bootstrap-secret"})

	created := pairingRequest(t, handler, http.MethodPost, "/api/v1/pairings", `{"name":"Phone","scopes":["read","control"],"qr":true}`)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", created.Code, created.Body.String())
	}
	var pairing struct {
		ID         string `json:"id"`
		Secret     string `json:"secret"`
		PairingURL string `json:"pairingURL"`
		QR         string `json:"qr"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &pairing); err != nil {
		t.Fatal(err)
	}
	encoded, ok := strings.CutPrefix(pairing.QR, "data:image/png;base64,")
	if !ok {
		t.Fatalf("qr=%.40q, want a PNG data URL", pairing.QR)
	}
	image, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(image)); err != nil {
		t.Fatalf("the pairing QR is not a PNG: %v", err)
	}
	if plain := pairingRequest(t, handler, http.MethodPost, "/api/v1/pairings", `{"name":"Phone","scopes":["read","control"]}`); strings.Contains(plain.Body.String(), `"qr"`) {
		t.Fatalf("a pairing that asked for no QR carried one: %s", plain.Body.String())
	}

	status := func() registry.DevicePairingStatus {
		t.Helper()
		response := pairingRequest(t, handler, http.MethodGet, "/api/v1/pairings?id="+pairing.ID, "")
		if response.Code != http.StatusOK {
			t.Fatalf("status code=%d body=%s", response.Code, response.Body.String())
		}
		var status registry.DevicePairingStatus
		if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil {
			t.Fatal(err)
		}
		return status
	}
	if pending := status(); pending.State != registry.PairingPending || pending.Device != nil {
		t.Fatalf("before pairing=%+v", pending)
	}

	completeBody, _ := json.Marshal(map[string]string{"secret": pairing.Secret, "name": "My iPhone"})
	completed := httptest.NewRecorder()
	handler.ServeHTTP(completed, httptest.NewRequest(http.MethodPost, "/api/v1/pair", bytes.NewReader(completeBody)))
	if completed.Code != http.StatusCreated {
		t.Fatalf("complete status=%d body=%s", completed.Code, completed.Body.String())
	}
	var answer struct {
		Device registry.Device `json:"device"`
		Host   struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"host"`
	}
	if err := json.Unmarshal(completed.Body.Bytes(), &answer); err != nil {
		t.Fatal(err)
	}
	if answer.Host.ID != "h0123456789ab" || answer.Host.Name != "server" {
		t.Fatalf("pairing named host %+v, want h0123456789ab server", answer.Host)
	}
	paired := status()
	if paired.State != registry.PairingPaired || paired.Device == nil || paired.Device.ID != answer.Device.ID || paired.Device.Name != "My iPhone" {
		t.Fatalf("after pairing=%+v, want device %s", paired, answer.Device.ID)
	}

	if missing := pairingRequest(t, handler, http.MethodGet, "/api/v1/pairings?id=no-such-pairing", ""); missing.Code != http.StatusNotFound || !strings.Contains(missing.Body.String(), "pairing_not_found") {
		t.Fatalf("unknown pairing code=%d body=%s", missing.Code, missing.Body.String())
	}
}

func TestAPIV1PairingCodeNobodyUsedExpires(t *testing.T) {
	d, reg, _, _, _ := daemonFixture(t)
	handler := d.dashboardHandler(&dashboardServer{token: "bootstrap-secret"})
	pairing, err := reg.CreateDevicePairing("Phone", []string{"read", "control"}, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	response := pairingRequest(t, handler, http.MethodGet, "/api/v1/pairings?id="+pairing.ID, "")
	var status registry.DevicePairingStatus
	if err := json.Unmarshal(response.Body.Bytes(), &status); err != nil || status.State != registry.PairingExpired {
		t.Fatalf("status=%+v err=%v body=%s", status, err, response.Body.String())
	}
}

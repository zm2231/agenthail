package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestABrowserManagesOnlyItsOwnPushSubscription(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	d, reg, _, _, _ := daemonFixture(t)
	handler := dashboardRouter(d)
	status := func() browserPushStatus {
		t.Helper()
		response := serveDashboardRequest(handler, http.MethodGet, "/api/v1/browser/push", "")
		var status browserPushStatus
		if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
			t.Fatalf("GET status=%d body=%s", response.Code, response.Body.String())
		}
		return status
	}
	keys, err := webPushKey()
	if err != nil {
		t.Fatal(err)
	}
	if got := status(); got.PublicKey != keys.PublicKey || got.Subscribed {
		t.Fatalf("status=%+v", got)
	}
	subscription := `{"endpoint":"https://push.example/capability","keys":{"p256dh":"p256dh","auth":"auth"}}`
	if response := serveDashboardRequest(handler, http.MethodPut, "/api/v1/browser/push", subscription); response.Code != http.StatusOK {
		t.Fatalf("PUT status=%d body=%s", response.Code, response.Body.String())
	}
	if got := status(); !got.Subscribed || !got.Withdraw {
		t.Fatalf("status=%+v", got)
	}
	for _, report := range []string{`{"endpoint":"https://push.example/capability","withdraw":true}`, `{"endpoint":"https://push.example/capability"}`, `{"withdraw":false}`} {
		if response := serveDashboardRequest(handler, http.MethodPatch, "/api/v1/browser/push", report); response.Code != http.StatusBadRequest {
			t.Fatalf("PATCH %s status=%d", report, response.Code)
		}
	}
	if response := serveDashboardRequest(handler, http.MethodPatch, "/api/v1/browser/push", `{"endpoint":"https://push.example/capability","withdraw":false}`); response.Code != http.StatusOK {
		t.Fatalf("PATCH status=%d body=%s", response.Code, response.Body.String())
	}
	if got := status(); !got.Subscribed || got.Withdraw {
		t.Fatalf("after the report status=%+v", got)
	}
	if body := serveDashboardRequest(handler, http.MethodGet, "/api/v1/devices", "").Body.String(); strings.Contains(body, "push.example") || !strings.Contains(body, `"pushEnabled":true`) {
		t.Fatalf("devices list: %s", body)
	}
	if response := serveDashboardRequest(handler, http.MethodPut, "/api/v1/browser/push", `{"endpoint":"http://push.example/plain","keys":{"p256dh":"p256dh","auth":"auth"}}`); response.Code != http.StatusBadRequest {
		t.Fatalf("plain endpoint status=%d", response.Code)
	}
	crossOrigin := httptest.NewRequest(http.MethodDelete, "/api/v1/browser/push", nil)
	crossOrigin.Host = "example.test"
	crossOrigin.Header.Set("Origin", "https://elsewhere.test")
	addBrowserCookie(handler, crossOrigin)
	crossOriginResponse := httptest.NewRecorder()
	handler.ServeHTTP(crossOriginResponse, crossOrigin)
	if crossOriginResponse.Code != http.StatusForbidden || !status().Subscribed {
		t.Fatalf("cross-origin DELETE status=%d", crossOriginResponse.Code)
	}
	pairing, err := reg.CreateDevicePairing("Phone", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, phoneToken, err := reg.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, token := range map[string]string{"dashboard token": dashboardTestToken, "phone": phoneToken} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/browser/push", nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("%s status=%d", name, response.Code)
		}
	}
	if response := serveDashboardRequest(handler, http.MethodDelete, "/api/v1/browser/push", ""); response.Code != http.StatusOK || status().Subscribed {
		t.Fatalf("DELETE status=%d", response.Code)
	}
}

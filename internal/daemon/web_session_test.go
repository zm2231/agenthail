package daemon

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func pairWebDevice(t *testing.T, reg *registry.Registry, scopes ...string) (registry.Device, string) {
	t.Helper()
	pairing, err := reg.CreateDevicePairing("Phone", scopes, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := reg.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	return device, token
}

func mintWebSession(t *testing.T, handler http.Handler, bearer string) webSessionView {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/web-session", nil)
	request.Header.Set("Authorization", "Bearer "+bearer)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("minting a web session answered %d %s", recorder.Code, recorder.Body)
	}
	var view webSessionView
	if err := json.Unmarshal(recorder.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if view.Name != webSessionCookieName || view.Path != "/" || !strings.HasPrefix(view.Value, webSessionPrefix) {
		t.Fatalf("the web session is %+v", view)
	}
	return view
}

func webSessionCall(handler http.Handler, method, path string, session webSessionView, headers map[string]string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, path, strings.NewReader("{}"))
	request.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

func TestAWebSessionOpensExtensionPagesAndTheEventStreamAndNothingElse(t *testing.T) {
	_, reg, _, _, handler, _ := baseFixture(t)
	_, token := pairWebDevice(t, reg, "read", "control")
	session := mintWebSession(t, handler, token)
	if !slices.Equal(session.Scopes, []string{"read", "control"}) {
		t.Fatalf("scopes %v", session.Scopes)
	}
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusOK {
		t.Fatalf("the extension SDK answered %d %s", got.Code, got.Body)
	}
	if got := webSessionCall(handler, http.MethodPost, "/x/missing/a/run", session, nil); got.Code != http.StatusNotFound {
		t.Fatalf("an action on a missing extension answered %d, want the handler's 404", got.Code)
	}
	for _, path := range []string{"/api/v1/extensions", "/api/v1/snapshot", "/api/v1/hosts", "/api/v1/version", "/"} {
		if got := webSessionCall(handler, http.MethodGet, path, session, nil); got.Code != http.StatusUnauthorized {
			t.Fatalf("GET %s with only a web session answered %d", path, got.Code)
		}
	}
	if got := webSessionCall(handler, http.MethodPost, "/api/v1/web-session", session, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("a web session minted another with %d", got.Code)
	}
	if got := webSessionCall(handler, http.MethodPost, "/api/v1/extensions", session, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("a web session reloaded extensions with %d", got.Code)
	}
	if got := webSessionCall(handler, http.MethodPost, "/x/missing/a/run", session, map[string]string{"Origin": "http://elsewhere.example"}); got.Code != http.StatusForbidden {
		t.Fatalf("a cross-origin action answered %d", got.Code)
	}
	forged := session
	forged.Value = webSessionPrefix + "forged"
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", forged, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("an unknown web session answered %d", got.Code)
	}

	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+"/api/v1/events", nil)
	request.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("the event stream answered %d %q", response.StatusCode, response.Header.Get("Content-Type"))
	}
}

func TestAWebSessionHoldsNoMoreThanTheCredentialThatMintedIt(t *testing.T) {
	_, reg, _, dashboard, handler, _ := baseFixture(t)
	_, readOnly := pairWebDevice(t, reg, "read")
	session := mintWebSession(t, handler, readOnly)
	if !slices.Equal(session.Scopes, []string{"read"}) {
		t.Fatalf("a read-only device minted %v", session.Scopes)
	}
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusOK {
		t.Fatalf("a read-only page load answered %d", got.Code)
	}
	if got := webSessionCall(handler, http.MethodPost, "/x/missing/a/run", session, nil); got.Code != http.StatusForbidden {
		t.Fatalf("a read-only web session ran an action with %d", got.Code)
	}
	if full := mintWebSession(t, handler, dashboard.token); !slices.Equal(full.Scopes, []string{"read", "control"}) {
		t.Fatalf("the dashboard token minted %v", full.Scopes)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/web-session", nil)
	signedIn, err := signInBrowser(reg)
	if err != nil {
		t.Fatal(err)
	}
	request.AddCookie(signedIn)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("a dashboard cookie minted a web session with %d", recorder.Code)
	}
}

func TestRevokingTheMintingDeviceEndsItsWebSessionsAtTheNextRequest(t *testing.T) {
	_, reg, _, _, handler, _ := baseFixture(t)
	device, token := pairWebDevice(t, reg, "read", "control")
	session := mintWebSession(t, handler, token)
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusOK {
		t.Fatalf("before revoking: %d", got.Code)
	}
	if err := reg.RevokeDevice(device.ID); err != nil {
		t.Fatal(err)
	}
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("the first request after revoking answered %d", got.Code)
	}
}

func TestAWebSessionEndsAfterItsLifetime(t *testing.T) {
	d, reg, _, _, handler, _ := baseFixture(t)
	now := time.Now()
	d.webSessions.now = func() time.Time { return now }
	_, token := pairWebDevice(t, reg, "read")
	session := mintWebSession(t, handler, token)
	if !session.ExpiresAt.Equal(now.Add(webSessionLifetime).UTC()) {
		t.Fatalf("expires %v", session.ExpiresAt)
	}
	now = now.Add(webSessionLifetime - time.Second)
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusOK {
		t.Fatalf("just before expiry: %d", got.Code)
	}
	now = now.Add(time.Second)
	if got := webSessionCall(handler, http.MethodGet, "/x/_ah.js", session, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("at expiry: %d", got.Code)
	}
}

func TestAMountPortTakesAWebSessionForItsAppOnlyAndKeepsTheCookieFromIt(t *testing.T) {
	upstream, seen := startUpstream(t)
	f := startMountFixture(t)
	f.declare(t, upstream.URL)
	f.approve(t)
	mount := f.mount(t)
	_, full := pairWebDevice(t, f.reg, "read", "control")
	_, readOnly := pairWebDevice(t, f.reg, "read")
	session := mintWebSession(t, f.handler, full)
	reader := mintWebSession(t, f.handler, readOnly)
	base := "http://127.0.0.1:" + strconv.Itoa(mount.Port)
	call := func(method string, session *webSessionView) int {
		t.Helper()
		request, err := http.NewRequest(method, base+"/", strings.NewReader("{}"))
		if err != nil {
			t.Fatal(err)
		}
		if session != nil {
			request.AddCookie(&http.Cookie{Name: session.Name, Value: session.Value})
		}
		response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return response.StatusCode
	}
	if status := call(http.MethodGet, nil); status != http.StatusUnauthorized {
		t.Fatalf("no cookie: %d", status)
	}
	if status := call(http.MethodGet, &session); status != http.StatusOK {
		t.Fatalf("GET with a web session: %d", status)
	}
	if cookies := seen.last(t).Header.Get("Cookie"); strings.Contains(cookies, webSessionCookieName) {
		t.Fatalf("the upstream got the web session cookie: %q", cookies)
	}
	if status := call(http.MethodPost, &session); status != http.StatusOK {
		t.Fatalf("POST with a control web session: %d", status)
	}
	if status := call(http.MethodPost, &reader); status != http.StatusForbidden {
		t.Fatalf("POST with a read-only web session: %d", status)
	}
}

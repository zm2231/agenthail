package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestActionIdempotencyReplaysCanonicalRequestAndRejectsMismatch(t *testing.T) {
	r := openActionTestRegistry(t)
	d := New(r, nil)
	var effects atomic.Int32
	next := func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true,"receipt":"r1"}`))
	}
	handler := d.idempotentActionHandler(next)
	first := actionRequest(`{"b":2,"a":1}`)
	first.Header.Set("Idempotency-Key", "key-1")
	first = first.WithContext(context.WithValue(first.Context(), actionPrincipalContextKey{}, "device-a"))
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusCreated || effects.Load() != 1 {
		t.Fatalf("first status=%d effects=%d body=%s", firstResponse.Code, effects.Load(), firstResponse.Body.String())
	}
	second := actionRequest(`{"a":1,"b":2}`)
	second.Header.Set("Idempotency-Key", "key-1")
	second = second.WithContext(context.WithValue(second.Context(), actionPrincipalContextKey{}, "device-a"))
	secondResponse := httptest.NewRecorder()
	handler.ServeHTTP(secondResponse, second)
	if secondResponse.Code != http.StatusCreated || secondResponse.Body.String() != firstResponse.Body.String() || effects.Load() != 1 {
		t.Fatalf("replay status=%d effects=%d body=%s", secondResponse.Code, effects.Load(), secondResponse.Body.String())
	}
	mismatch := actionRequest(`{"a":2,"b":1}`)
	mismatch.Header.Set("Idempotency-Key", "key-1")
	mismatch = mismatch.WithContext(context.WithValue(mismatch.Context(), actionPrincipalContextKey{}, "device-a"))
	mismatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusConflict || effects.Load() != 1 {
		t.Fatalf("mismatch status=%d effects=%d body=%s", mismatchResponse.Code, effects.Load(), mismatchResponse.Body.String())
	}
}

func TestActionIdempotencyPendingAndRestartNeverRedispatches(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	d := New(r, nil)
	started := make(chan struct{})
	release := make(chan struct{})
	var effects atomic.Int32
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	firstResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		request := actionRequest(`{"action":"send","message":"one"}`)
		request.Header.Set("Idempotency-Key", "inflight")
		firstResponse <- serveAction(handler, request, "device-a")
	}()
	<-started
	pending := serveAction(handler, actionRequestWithKey(`{"action":"send","message":"one"}`, "inflight"), "device-a")
	if pending.Code != http.StatusAccepted || effects.Load() != 1 {
		t.Fatalf("pending status=%d effects=%d body=%s", pending.Code, effects.Load(), pending.Body.String())
	}
	if pending.Body.String() != `{"ok":true,"status":"submitted"}
` {
		t.Fatalf("pending body=%q", pending.Body.String())
	}
	close(release)
	if response := <-firstResponse; response.Code != http.StatusOK {
		t.Fatalf("first completion status=%d", response.Code)
	}

	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	hash := sha256.Sum256([]byte(`{"action":"send"}`))
	if _, err := second.ReserveActionReceipt("device-b", "restart", hex.EncodeToString(hash[:]), time.Now()); err != nil {
		t.Fatal(err)
	}
	var restartedEffects atomic.Int32
	restarted := New(second, nil).idempotentActionHandler(func(http.ResponseWriter, *http.Request) { restartedEffects.Add(1) })
	response := serveAction(restarted, actionRequestWithKey(`{"action":"send"}`, "restart"), "device-b")
	if response.Code != http.StatusAccepted || restartedEffects.Load() != 0 {
		t.Fatalf("restart status=%d effects=%d body=%s", response.Code, restartedEffects.Load(), response.Body.String())
	}
}

func TestActionIdempotencyReplaysTextFailureContentType(t *testing.T) {
	r := openActionTestRegistry(t)
	d := New(r, nil)
	var effects atomic.Int32
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		http.Error(w, "provider unavailable", http.StatusBadGateway)
	})
	first := serveAction(handler, actionRequestWithKey(`{"action":"send"}`, "text-failure"), "device-a")
	second := serveAction(handler, actionRequestWithKey(`{"action":"send"}`, "text-failure"), "device-a")
	if first.Code != http.StatusBadGateway || second.Code != first.Code || second.Body.String() != first.Body.String() || second.Header().Get("Content-Type") != first.Header().Get("Content-Type") || effects.Load() != 1 {
		t.Fatalf("first=%d/%q second=%d/%q content-type=%q/%q effects=%d", first.Code, first.Body.String(), second.Code, second.Body.String(), first.Header().Get("Content-Type"), second.Header().Get("Content-Type"), effects.Load())
	}
}

func TestActionIdempotencyResponseOverflowLeavesPending(t *testing.T) {
	r := openActionTestRegistry(t)
	d := New(r, nil)
	var effects atomic.Int32
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		_, _ = w.Write(bytes.Repeat([]byte("x"), actionReplayBodyLimit+1))
	})
	first := serveAction(handler, actionRequestWithKey(`{"action":"large"}`, "overflow"), "device-a")
	second := serveAction(handler, actionRequestWithKey(`{"action":"large"}`, "overflow"), "device-a")
	if first.Code != http.StatusAccepted || second.Code != http.StatusAccepted || effects.Load() != 1 {
		t.Fatalf("first=%d second=%d effects=%d", first.Code, second.Code, effects.Load())
	}
	if first.Body.String() != `{"ok":true,"status":"submitted"}
` || second.Body.String() != first.Body.String() {
		t.Fatalf("first body=%q second body=%q", first.Body.String(), second.Body.String())
	}
}

func TestActionIdempotencyRunsAfterAPIAuthorization(t *testing.T) {
	r := openActionTestRegistry(t)
	d := New(r, nil)
	dashboard := &dashboardServer{token: "dashboard-secret"}
	var effects atomic.Int32
	next := d.apiV1Guard(dashboard, "control", d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	}))
	unauthorized := actionRequestWithKey(`{"action":"send"}`, "auth-order")
	unauthorizedResponse := httptest.NewRecorder()
	next(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized || effects.Load() != 0 {
		t.Fatalf("unauthorized status=%d effects=%d", unauthorizedResponse.Code, effects.Load())
	}
	authorized := actionRequestWithKey(`{"action":"send"}`, "auth-order")
	authorized.Header.Set("Authorization", "Bearer dashboard-secret")
	authorizedResponse := httptest.NewRecorder()
	next(authorizedResponse, authorized)
	if authorizedResponse.Code != http.StatusOK || effects.Load() != 1 {
		t.Fatalf("authorized status=%d effects=%d body=%s", authorizedResponse.Code, effects.Load(), authorizedResponse.Body.String())
	}
}

func TestDashboardActionRouteUsesIdempotencyWrapper(t *testing.T) {
	r := openActionTestRegistry(t)
	d := New(r, nil)
	handler := d.dashboardHandler(&dashboardServer{token: "dashboard-secret"})
	first := actionRequestWithKey(`{"action":"unsupported"}`, "dashboard-route")
	first.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "dashboard-secret"})
	firstResponse := httptest.NewRecorder()
	handler.ServeHTTP(firstResponse, first)
	if firstResponse.Code != http.StatusBadRequest {
		t.Fatalf("first status=%d body=%s", firstResponse.Code, firstResponse.Body.String())
	}
	mismatch := actionRequestWithKey(`{"action":"different"}`, "dashboard-route")
	mismatch.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: "dashboard-secret"})
	mismatchResponse := httptest.NewRecorder()
	handler.ServeHTTP(mismatchResponse, mismatch)
	if mismatchResponse.Code != http.StatusConflict || effectsInBody(mismatchResponse.Body.Bytes(), "action_idempotency_mismatch") == false {
		t.Fatalf("mismatch status=%d body=%s", mismatchResponse.Code, mismatchResponse.Body.String())
	}
}

func effectsInBody(body []byte, value string) bool {
	return bytes.Contains(body, []byte(value))
}

func TestActionIdempotencyCompletionFailureLeavesPending(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var effects atomic.Int32
	d := New(r, nil)
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		_, _ = w.Write([]byte(`{"ok":true}`))
		_ = r.Close()
	})
	response := serveAction(handler, actionRequestWithKey(`{"action":"send"}`, "write-fails"), "device-a")
	if response.Code != http.StatusAccepted || effects.Load() != 1 {
		t.Fatalf("completion failure status=%d effects=%d body=%s", response.Code, effects.Load(), response.Body.String())
	}
	second, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	retriedEffects := atomic.Int32{}
	retried := New(second, nil).idempotentActionHandler(func(http.ResponseWriter, *http.Request) { retriedEffects.Add(1) })
	retryResponse := serveAction(retried, actionRequestWithKey(`{"action":"send"}`, "write-fails"), "device-a")
	if retryResponse.Code != http.StatusAccepted || retriedEffects.Load() != 0 {
		t.Fatalf("retry status=%d effects=%d body=%s", retryResponse.Code, retriedEffects.Load(), retryResponse.Body.String())
	}
}

func openActionTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	return r
}

func actionRequest(body string) *http.Request {
	return httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewBufferString(body))
}

func actionRequestWithKey(body, key string) *http.Request {
	request := actionRequest(body)
	request.Header.Set("Idempotency-Key", key)
	return request
}

func serveAction(handler http.HandlerFunc, request *http.Request, principal string) *httptest.ResponseRecorder {
	request = request.WithContext(context.WithValue(request.Context(), actionPrincipalContextKey{}, principal))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

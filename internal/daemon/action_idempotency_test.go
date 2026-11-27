package daemon

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestActionIdempotencyOversizedReplyReplaysItsStatus(t *testing.T) {
	r := openActionTestRegistry(t)
	d := newTestDaemon(t, r, nil)
	var effects atomic.Int32
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		effects.Add(1)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(bytes.Repeat([]byte("x"), actionReplayBodyLimit+1))
	})
	first := serveAction(handler, actionRequestWithKey(`{"action":"large"}`, "overflow"), "device-a")
	second := serveAction(handler, actionRequestWithKey(`{"action":"large"}`, "overflow"), "device-a")
	if first.Code != http.StatusCreated || first.Body.Len() != actionReplayBodyLimit+1 || effects.Load() != 1 {
		t.Fatalf("first=%d len=%d effects=%d", first.Code, first.Body.Len(), effects.Load())
	}
	var replay struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if second.Code != http.StatusCreated || json.Unmarshal(second.Body.Bytes(), &replay) != nil || !replay.OK || replay.Status != "completed" {
		t.Fatalf("resend=%d body=%s, want the stored status with a short body", second.Code, second.Body.String())
	}
}

func dashboardActionRequestWithKey(body, key string) *http.Request {
	request := httptest.NewRequest(http.MethodPost, "/api/action", bytes.NewBufferString(body))
	request.Host = "127.0.0.1:7412"
	request.Header.Set("Origin", "http://127.0.0.1:7412")
	request.Header.Set("Idempotency-Key", key)
	return request
}

func openActionTestRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	r, err := registry.Open(registrytest.Path(t))
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

type hookedSendSurface struct {
	*daemonSurface
	beforeSend func()
}

func (s *hookedSendSurface) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	s.beforeSend()
	return s.daemonSurface.Send(ctx, session, message)
}

func (s *hookedSendSurface) SendWithOptions(ctx context.Context, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	s.beforeSend()
	return s.daemonSurface.SendWithOptions(ctx, session, message, options)
}

func openIdempotencyFixture(t *testing.T, path string, adapter surface.Surface) (*registry.Registry, http.Handler) {
	t.Helper()
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = r.Close() })
	if err := r.RecordObservedSession(surface.Session{ID: "from", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}); err != nil {
		t.Fatal(err)
	}
	return r, dashboardRouter(newTestDaemon(t, r, []surface.Surface{adapter}))
}

func idempotencyFake() *daemonSurface {
	session := surface.Session{ID: "from", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	return &daemonSurface{sessions: map[string]surface.Session{"from": session}, observations: map[string]*surface.TurnObservation{}, accepted: true}
}

func pairedControlToken(t *testing.T, r *registry.Registry) string {
	t.Helper()
	pairing, err := r.CreateDevicePairing("Phone", []string{"read", "control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, "Phone", "")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func serveV1Action(handler http.Handler, body, key, token string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewBufferString(body))
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	request.Header.Set(actionIdempotencyHeader, key)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func assertActionSubmitted(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	var body struct {
		OK     bool   `json:"ok"`
		Status string `json:"status"`
	}
	if response.Code != http.StatusAccepted || json.Unmarshal(response.Body.Bytes(), &body) != nil || !body.OK || body.Status != "submitted" {
		t.Fatalf("status=%d body=%s, want submitted receipt", response.Code, response.Body.String())
	}
}

func TestActionIdempotencyReplaysCanonicalRequestAndRejectsMismatch(t *testing.T) {
	fake := idempotencyFake()
	r, handler := openIdempotencyFixture(t, registrytest.Path(t), fake)
	token := pairedControlToken(t, r)
	first := serveV1Action(handler, `{"message":"one","sessionId":"from","action":"send"}`, "key-1", token)
	if first.Code != http.StatusOK || len(fake.sent) != 1 {
		t.Fatalf("first status=%d sends=%d body=%s", first.Code, len(fake.sent), first.Body.String())
	}
	replay := serveV1Action(handler, `{"action":"send","sessionId":"from","message":"one"}`, "key-1", token)
	if replay.Code != first.Code || replay.Body.String() != first.Body.String() || len(fake.sent) != 1 {
		t.Fatalf("replay status=%d sends=%d body=%s", replay.Code, len(fake.sent), replay.Body.String())
	}
	mismatch := serveV1Action(handler, `{"action":"send","sessionId":"from","message":"two"}`, "key-1", token)
	assertAPIV1Error(t, mismatch, http.StatusConflict, "action_idempotency_mismatch")
	if len(fake.sent) != 1 {
		t.Fatalf("mismatch sends=%d", len(fake.sent))
	}
}

func TestActionIdempotencyInterruptedRunIsUnknownNotRedispatched(t *testing.T) {
	path := registrytest.Path(t)
	started := make(chan struct{})
	release := make(chan struct{})
	fake := idempotencyFake()
	adapter := &hookedSendSurface{daemonSurface: fake, beforeSend: func() {
		close(started)
		<-release
	}}
	r, handler := openIdempotencyFixture(t, path, adapter)
	token := pairedControlToken(t, r)
	const body = `{"action":"send","sessionId":"from","message":"one"}`
	firstResponse := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstResponse <- serveV1Action(handler, body, "inflight", token) }()
	<-started
	assertActionSubmitted(t, serveV1Action(handler, body, "inflight", token))
	close(release)
	if response := <-firstResponse; response.Code != http.StatusOK || len(fake.sent) != 1 {
		t.Fatalf("first completion status=%d sends=%d body=%s", response.Code, len(fake.sent), response.Body.String())
	}

	device, err := r.AuthenticateDevice(token, "control")
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256([]byte(`{"action":"send","message":"restart","sessionId":"from"}`))
	if _, err := r.ReserveActionReceipt(device.ID, "restart", hex.EncodeToString(hash[:]), time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	restartedFake := idempotencyFake()
	_, restarted := openIdempotencyFixture(t, path, restartedFake)
	for range 2 {
		response := serveV1Action(restarted, `{"action":"send","sessionId":"from","message":"restart"}`, "restart", token)
		assertAPIV1Error(t, response, http.StatusConflict, "action_outcome_unknown")
		if !bytes.Contains(response.Body.Bytes(), []byte("transcript")) {
			t.Fatalf("an interrupted send does not say where to check: %s", response.Body.String())
		}
	}
	if len(restartedFake.sent) != 0 {
		t.Fatalf("restarted daemon re-dispatched a reserved action: sends=%d", len(restartedFake.sent))
	}
}

func TestActionIdempotencyReplaysTextFailureContentType(t *testing.T) {
	fake := idempotencyFake()
	fake.sendErr = surface.DeliveryTerminal(errors.New("provider unavailable"), surface.DeliveryInvalidRequest)
	store, handler := openIdempotencyFixture(t, registrytest.Path(t), fake)
	cookie := browserCookie(t, store)
	send := func() *httptest.ResponseRecorder {
		request := dashboardActionRequestWithKey(`{"action":"send","sessionId":"from","message":"one"}`, "text-failure")
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	first := send()
	second := send()
	if first.Code < 400 || second.Code != first.Code || second.Body.String() != first.Body.String() || second.Header().Get("Content-Type") != first.Header().Get("Content-Type") || len(fake.sent) != 1 {
		t.Fatalf("first=%d/%q second=%d/%q content-type=%q/%q sends=%d", first.Code, first.Body.String(), second.Code, second.Body.String(), first.Header().Get("Content-Type"), second.Header().Get("Content-Type"), len(fake.sent))
	}
}

func TestActionIdempotencyRunsAfterAPIAuthorization(t *testing.T) {
	fake := idempotencyFake()
	r, handler := openIdempotencyFixture(t, registrytest.Path(t), fake)
	token := pairedControlToken(t, r)
	const body = `{"action":"send","sessionId":"from","message":"one"}`
	assertAPIV1Error(t, serveV1Action(handler, body, "auth-order", ""), http.StatusUnauthorized, "unauthorized")
	if len(fake.sent) != 0 {
		t.Fatalf("unauthorized request sent=%d", len(fake.sent))
	}
	if response := serveV1Action(handler, body, "auth-order", token); response.Code != http.StatusOK || len(fake.sent) != 1 {
		t.Fatalf("authorized status=%d sends=%d body=%s", response.Code, len(fake.sent), response.Body.String())
	}
}

func TestActionIdempotencyLostCompletionIsUnknownNotRedispatched(t *testing.T) {
	path := registrytest.Path(t)
	fake := idempotencyFake()
	var r *registry.Registry
	adapter := &hookedSendSurface{daemonSurface: fake, beforeSend: func() { _ = r.Close() }}
	r, handler := openIdempotencyFixture(t, path, adapter)
	token := pairedControlToken(t, r)
	const body = `{"action":"send","sessionId":"from","message":"one"}`
	serveV1Action(handler, body, "write-fails", token)
	if len(fake.sent) != 1 {
		t.Fatalf("sends=%d", len(fake.sent))
	}
	retriedFake := idempotencyFake()
	_, retried := openIdempotencyFixture(t, path, retriedFake)
	assertAPIV1Error(t, serveV1Action(retried, body, "write-fails", token), http.StatusConflict, "action_outcome_unknown")
	if len(retriedFake.sent) != 0 {
		t.Fatalf("retry after lost completion re-dispatched: sends=%d", len(retriedFake.sent))
	}
}

func TestActionIdempotencyUnservedRequestRunsAgainUnderItsKey(t *testing.T) {
	r := openActionTestRegistry(t)
	d := newTestDaemon(t, r, nil)
	var calls atomic.Int32
	handler := d.idempotentActionHandler(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			writeAPIError(w, http.StatusServiceUnavailable, "ledger_owner_unreachable", "The ledger's host cannot be reached; nothing was recorded.")
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	first := serveAction(handler, actionRequestWithKey(`{"text":"add"}`, "owner-down"), "device-a")
	second := serveAction(handler, actionRequestWithKey(`{"text":"add"}`, "owner-down"), "device-a")
	third := serveAction(handler, actionRequestWithKey(`{"text":"add"}`, "owner-down"), "device-a")
	if first.Code != http.StatusServiceUnavailable || second.Code != http.StatusOK || third.Code != http.StatusOK || calls.Load() != 2 {
		t.Fatalf("first=%d second=%d third=%d calls=%d", first.Code, second.Code, third.Code, calls.Load())
	}
}

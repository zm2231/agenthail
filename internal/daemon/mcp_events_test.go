package daemon

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

const webhookSecret = "whsec_MfKQ9r8GKYqrTwjUPD8ILPZIo2LaLaSw"

type webhookCall struct {
	id, timestamp, subscription string
	body                        map[string]any
}

type webhookReceiver struct {
	t      *testing.T
	server *httptest.Server
	url    string
	calls  chan webhookCall
	mu     sync.Mutex
	secret string
	reply  func(call webhookCall) (int, string)
	gate   chan struct{}
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	w := &webhookReceiver{t: t, calls: make(chan webhookCall, 64), secret: webhookSecret}
	w.reply = func(call webhookCall) (int, string) {
		if call.body["type"] == "verification" {
			return http.StatusOK, fmt.Sprintf(`{"challenge":%q}`, call.body["challenge"])
		}
		return http.StatusOK, ""
	}
	w.server = httptest.NewTLSServer(http.HandlerFunc(w.serve))
	t.Cleanup(w.server.Close)
	w.url = "https://example.com:" + w.server.Listener.Addr().(*net.TCPAddr).AddrPort().String()[len("127.0.0.1:"):]
	return w
}

// client reaches the receiver under a public name its certificate holds, bypassing the address guard.
func (w *webhookReceiver) client() *http.Client {
	transport := w.server.Client().Transport.(*http.Transport).Clone()
	address := w.server.Listener.Addr().String()
	transport.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	return &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

func (w *webhookReceiver) serve(rw http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	call := webhookCall{id: r.Header.Get("webhook-id"), timestamp: r.Header.Get("webhook-timestamp"), subscription: r.Header.Get("X-MCP-Subscription-Id")}
	w.mu.Lock()
	secret, reply, gate := w.secret, w.reply, w.gate
	w.mu.Unlock()
	if !validWebhookSignature(secret, call.id, call.timestamp, body, r.Header.Get("webhook-signature")) || r.Header.Get("Content-Type") != "application/json" {
		w.t.Errorf("a webhook failed verification: id=%q signature=%q", call.id, r.Header.Get("webhook-signature"))
		rw.WriteHeader(http.StatusUnauthorized)
		return
	}
	if err := json.Unmarshal(body, &call.body); err != nil {
		w.t.Errorf("webhook body %s: %v", body, err)
	}
	if gate != nil && call.body["type"] == nil {
		<-gate
	}
	status, text := reply(call)
	w.calls <- call
	rw.WriteHeader(status)
	_, _ = io.WriteString(rw, text)
}

func validWebhookSignature(secret, id, timestamp string, body []byte, header string) bool {
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(seconds, 0)).Abs() > 5*time.Minute {
		return false
	}
	key, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(secret, "whsec_"))
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(id + "." + timestamp + "."))
	mac.Write(body)
	want := mac.Sum(nil)
	for _, signature := range strings.Fields(header) {
		encoded, ok := strings.CutPrefix(signature, "v1,")
		if got, err := base64.StdEncoding.DecodeString(encoded); ok && err == nil && hmac.Equal(got, want) {
			return true
		}
	}
	return false
}

func (w *webhookReceiver) next(t *testing.T, what string) webhookCall {
	t.Helper()
	select {
	case call := <-w.calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatalf("%s: no webhook arrived", what)
	}
	return webhookCall{}
}

func (w *webhookReceiver) quiet(t *testing.T, what string, wait time.Duration) {
	t.Helper()
	select {
	case call := <-w.calls:
		t.Fatalf("%s: unexpected webhook %+v", what, call)
	case <-time.After(wait):
	}
}

func (w *webhookReceiver) answer(reply func(webhookCall) (int, string)) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.reply = reply
}

func eventsFixture(t *testing.T) (*remoteFixture, *webhookReceiver, func()) {
	t.Helper()
	f := newRemoteFixture(t, "client-a")
	w := newWebhookReceiver(t)
	m := f.d.remoteMCP()
	m.webhookClient = w.client()
	m.eventRetries = []time.Duration{0, 20 * time.Millisecond, 20 * time.Millisecond}
	m.eventTrialEvery = 300 * time.Millisecond
	return f, w, func() { f.deliveries(t) }
}

func (f *remoteFixture) deliveries(t *testing.T) context.CancelFunc {
	t.Helper()
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { f.d.runEventDeliveries(ctx); close(done) }()
	stop := func() { cancel(); <-done }
	t.Cleanup(stop)
	return stop
}

type firstReply struct {
	headerTransport
	mu    sync.Mutex
	first []byte
}

func (c *firstReply) RoundTrip(r *http.Request) (*http.Response, error) {
	resp, err := c.headerTransport.RoundTrip(r)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil || c.first != nil {
		return resp, err
	}
	c.first, err = io.ReadAll(resp.Body)
	resp.Body.Close()
	resp.Body = io.NopCloser(bytes.NewReader(c.first))
	return resp, err
}

func (f *remoteFixture) eventsSession(t *testing.T) *mcp.ClientSession {
	session, _ := f.eventsSessionWithInitialize(t)
	return session
}

func (f *remoteFixture) eventsSessionWithInitialize(t *testing.T) (*mcp.ClientSession, string) {
	t.Helper()
	client := mcp.NewClient(&mcp.Implementation{Name: "kat", Version: "1"}, nil)
	for _, err := range []error{
		mcp.AddSendingCustomMethod[*eventsListParams, *eventsListResult](client, "events/list"),
		mcp.AddSendingCustomMethod[*eventsSubscribeParams, *eventsSubscribeResult](client, "events/subscribe"),
		mcp.AddSendingCustomMethod[*eventsUnsubscribeParams, *eventsUnsubscribeResult](client, "events/unsubscribe"),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	capture := &firstReply{headerTransport: headerTransport{"Authorization": "Bearer " + f.token}}
	transport := &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: capture}, MaxRetries: -1, DisableStandaloneSSE: true}
	session, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session, string(capture.first)
}

func subscribe(ctx context.Context, session *mcp.ClientSession, callback, secret string, ttl string) (*eventsSubscribeResult, error) {
	params := &eventsSubscribeParams{Name: eventInboxUnread, Arguments: json.RawMessage(`{}`), Delivery: eventDelivery{Mode: "webhook", URL: callback, Secret: secret}}
	if ttl != "" {
		params.TTLMs = json.RawMessage(ttl)
	}
	return mcp.CallCustomMethod[*eventsSubscribeParams, *eventsSubscribeResult](ctx, session, "events/subscribe", params)
}

func rpcError(t *testing.T, err error) (int64, map[string]any) {
	t.Helper()
	var wire *jsonrpc.Error
	if !errors.As(err, &wire) {
		t.Fatalf("want a JSON-RPC error, got %v", err)
	}
	var data map[string]any
	_ = json.Unmarshal(wire.Data, &data)
	return wire.Code, data
}

func (f *remoteFixture) eventHistory(t *testing.T) string {
	t.Helper()
	entries, err := f.r.ListHistory(200, "")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, entry := range entries {
		if entry.Kind == historyMCPEvent {
			lines = append(lines, entry.Message+" => "+entry.Result)
		}
	}
	return strings.Join(lines, "\n")
}

func TestARemoteIdentityIsWokenBySignedWebhooksThatCarryOnlyACount(t *testing.T) {
	f, w, start := eventsFixture(t)
	session, initialized := f.eventsSessionWithInitialize(t)
	if !strings.Contains(initialized, `"events":{}`) || !strings.Contains(initialized, `"tools":`) {
		t.Fatalf("initialize reply=%s", initialized)
	}
	listed, err := mcp.CallCustomMethod[*eventsListParams, *eventsListResult](t.Context(), session, "events/list", &eventsListParams{})
	if err != nil || len(listed.Events) != 1 || listed.Events[0].Name != eventInboxUnread || listed.Events[0].Delivery[0] != "webhook" {
		t.Fatalf("events/list=%+v err=%v", listed, err)
	}
	callback := w.url + "/hooks/kat?token=receiver-credential"
	subscribed, err := subscribe(t.Context(), session, callback, webhookSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	handshake := w.next(t, "the verification handshake")
	if handshake.body["type"] != "verification" || handshake.subscription != subscribed.ID || !strings.HasPrefix(handshake.id, "msg_verification_") {
		t.Fatalf("handshake=%+v subscription=%s", handshake, subscribed.ID)
	}
	if subscribed.RefreshBefore != nil || subscribed.Cursor != nil || subscribed.Truncated {
		t.Fatalf("subscribe result=%+v", subscribed)
	}
	again, err := subscribe(t.Context(), session, callback, webhookSecret, "")
	if err != nil || again.ID != subscribed.ID {
		t.Fatalf("a refresh id=%v err=%v, want the same id", again, err)
	}
	w.quiet(t, "a refresh of a verified endpoint", 200*time.Millisecond)
	start()

	f.deliver(t, "a private first message")
	event := w.next(t, "the first message")
	if event.body["eventId"] != event.id || event.body["name"] != eventInboxUnread || event.subscription != subscribed.ID || event.body["cursor"] != nil {
		t.Fatalf("event=%+v", event)
	}
	if data, _ := event.body["data"].(map[string]any); len(data) != 1 || data["unread"] != float64(1) {
		t.Fatalf("event data=%v", event.body["data"])
	}
	if _, err := time.Parse(time.RFC3339, event.body["timestamp"].(string)); err != nil {
		t.Fatalf("timestamp %v: %v", event.body["timestamp"], err)
	}

	w.mu.Lock()
	w.gate = make(chan struct{})
	gate := w.gate
	w.mu.Unlock()
	f.deliver(t, "second")
	time.Sleep(200 * time.Millisecond)
	f.deliver(t, "third")
	f.deliver(t, "fourth")
	time.Sleep(200 * time.Millisecond)
	close(gate)
	if held := w.next(t, "the event held at the gate"); held.body["data"].(map[string]any)["unread"] != float64(2) {
		t.Fatalf("held event=%v", held.body)
	}
	if burst := w.next(t, "the coalesced burst"); burst.body["data"].(map[string]any)["unread"] != float64(4) {
		t.Fatalf("burst event=%v", burst.body)
	}
	w.quiet(t, "after the burst was told", 300*time.Millisecond)

	history := f.eventHistory(t)
	if !strings.Contains(history, "subscribed") || !strings.Contains(history, "delivered") || strings.Contains(history, "receiver-credential") || strings.Contains(history, strings.TrimPrefix(webhookSecret, "whsec_")) || strings.Contains(history, "private") {
		t.Fatalf("history:\n%s", history)
	}

	for range 2 {
		if _, err := mcp.CallCustomMethod[*eventsUnsubscribeParams, *eventsUnsubscribeResult](t.Context(), session, "events/unsubscribe", &eventsUnsubscribeParams{Name: eventInboxUnread, Delivery: eventDelivery{Mode: "webhook", URL: callback}}); err != nil {
			t.Fatal(err)
		}
	}
	f.deliver(t, "after unsubscribing")
	w.quiet(t, "after unsubscribing", 500*time.Millisecond)
}

func TestAWebhookIsRetriedWithItsEventIDAndA410EndsTheSubscription(t *testing.T) {
	f, w, start := eventsFixture(t)
	session := f.eventsSession(t)
	subscribed, err := subscribe(t.Context(), session, w.url+"/hook", webhookSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	w.next(t, "the verification handshake")
	attempts := 0
	w.answer(func(webhookCall) (int, string) {
		attempts++
		if attempts == 1 {
			return http.StatusServiceUnavailable, ""
		}
		return http.StatusOK, ""
	})
	start()
	f.deliver(t, "one")
	first, retried := w.next(t, "the first attempt"), w.next(t, "the retry")
	if first.id != retried.id || first.body["eventId"] != retried.body["eventId"] {
		t.Fatalf("a retry changed the event id: %s then %s", first.id, retried.id)
	}
	w.quiet(t, "after the retry succeeded", 200*time.Millisecond)

	w.answer(func(webhookCall) (int, string) { return http.StatusGone, "" })
	f.deliver(t, "two")
	w.next(t, "the event answered 410")
	w.quiet(t, "after 410", 300*time.Millisecond)
	testkit.Eventually(t, "the 410 removes the subscription", 5*time.Second, func() bool {
		_, live, err := f.r.LiveEventSubscription(subscribed.ID, time.Now())
		return err == nil && !live
	})
	if history := f.eventHistory(t); !strings.Contains(history, "disabled") {
		t.Fatalf("history:\n%s", history)
	}
}

func TestASubscriptionSuspendedForFailingIsRetriedOnATrialAndRecovers(t *testing.T) {
	f, w, start := eventsFixture(t)
	f.d.remoteMCP().eventRetries = []time.Duration{0}
	session := f.eventsSession(t)
	subscribed, err := subscribe(t.Context(), session, w.url+"/hook", webhookSecret, "")
	if err != nil {
		t.Fatal(err)
	}
	w.next(t, "the verification handshake")
	w.answer(func(webhookCall) (int, string) { return http.StatusInternalServerError, "" })
	start()
	f.deliver(t, "one")
	for i := range eventSuspendAfter {
		w.next(t, fmt.Sprintf("failed event %d", i+1))
	}
	testkit.Eventually(t, "the subscription is suspended", 5*time.Second, func() bool {
		return strings.Contains(f.eventHistory(t), "suspended")
	})
	w.answer(func(webhookCall) (int, string) { return http.StatusOK, "" })
	trial := w.next(t, "the trial delivery")
	if trial.body["data"].(map[string]any)["unread"] != float64(1) {
		t.Fatalf("trial=%v", trial.body)
	}
	sub, live, err := f.r.LiveEventSubscription(subscribed.ID, time.Now())
	testkit.Eventually(t, "the trial clears the suspension", 5*time.Second, func() bool {
		sub, live, err = f.r.LiveEventSubscription(subscribed.ID, time.Now())
		return err == nil && live && sub.SuspendedAt.IsZero() && sub.Failures == 0
	})
	f.deliver(t, "two")
	if next := w.next(t, "an event after recovery"); next.body["data"].(map[string]any)["unread"] != float64(2) {
		t.Fatalf("event after recovery=%v", next.body)
	}
}

func TestEventSubscribeRefusesCallbacksItCannotSafelyReach(t *testing.T) {
	f, w, _ := eventsFixture(t)
	session := f.eventsSession(t)
	for name, callback := range map[string]string{
		"http":            strings.Replace(w.url, "https://", "http://", 1) + "/hook",
		"private address": "https://10.0.0.7/hook",
		"loopback":        "https://127.0.0.1/hook",
		"credentials":     "https://user:pass@example.com/hook",
	} {
		if _, err := subscribe(t.Context(), session, callback, webhookSecret, ""); err == nil {
			t.Fatalf("%s: subscribed", name)
		} else if code, _ := rpcError(t, err); code != jsonrpc.CodeInvalidParams {
			t.Fatalf("%s: code %d", name, code)
		}
	}
	if _, err := subscribe(t.Context(), session, w.url+"/hook", "whsec_c2hvcnQ=", ""); err == nil {
		t.Fatal("a short secret subscribed")
	}

	f.d.remoteMCP().webhookClient = newWebhookClient()
	_, err := subscribe(t.Context(), session, "https://localhost:"+strings.Split(w.server.URL, ":")[2]+"/hook", webhookSecret, "")
	if code, data := rpcError(t, err); code != codeCallbackEndpoint || data["reason"] != "blocked_address" {
		t.Fatalf("a name that resolves to loopback: code=%d data=%v", code, data)
	}
	w.quiet(t, "the guard refuses before connecting", 100*time.Millisecond)

	f.d.remoteMCP().webhookClient = w.client()
	w.answer(func(webhookCall) (int, string) { return http.StatusOK, `{"challenge":"not the one sent"}` })
	_, err = subscribe(t.Context(), session, w.url+"/hook", webhookSecret, "")
	if code, data := rpcError(t, err); code != codeCallbackEndpoint || data["reason"] != "challenge_failed" {
		t.Fatalf("a wrong challenge: code=%d data=%v", code, data)
	}
	if history := f.eventHistory(t); !strings.Contains(history, "refused: challenge_failed") {
		t.Fatalf("history:\n%s", history)
	}
}

func TestEventSubscriptionsAreBoundedAndTheirLifetimeFollowsTTL(t *testing.T) {
	f, w, _ := eventsFixture(t)
	session := f.eventsSession(t)
	forever, err := subscribe(t.Context(), session, w.url+"/forever", webhookSecret, "null")
	if err != nil || forever.RefreshBefore != nil {
		t.Fatalf("ttlMs null: %+v err=%v", forever, err)
	}
	short, err := subscribe(t.Context(), session, w.url+"/short", webhookSecret, "1000")
	if err != nil {
		t.Fatal(err)
	}
	if at, _ := time.Parse(time.RFC3339, *short.RefreshBefore); time.Until(at) < eventMinTTL-time.Minute {
		t.Fatalf("ttlMs 1000 granted %s, want the floor", *short.RefreshBefore)
	}
	for i := 2; i < eventSubscriptionLimit; i++ {
		if _, err := subscribe(t.Context(), session, fmt.Sprintf("%s/hook-%d", w.url, i), webhookSecret, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err = subscribe(t.Context(), session, w.url+"/one-too-many", webhookSecret, "")
	if code, data := rpcError(t, err); code != jsonrpc.CodeInvalidParams || data["limit"] != "subscriptions" || data["max"] != float64(eventSubscriptionLimit) {
		t.Fatalf("over the limit: code=%d data=%v", code, data)
	}
}

func TestACredentialChangeEndsEventDeliveryAndASubscriptionOutlivesARestart(t *testing.T) {
	changes := map[string]func(*remoteFixture) error{
		"rotate":          func(f *remoteFixture) error { _, _, err := f.r.RotateRemoteIdentity("scout"); return err },
		"off":             func(f *remoteFixture) error { return f.r.UnbindRemoteIdentity("scout") },
		"remove identity": func(f *remoteFixture) error { return f.r.RemoveAgentIdentity("scout") },
		"access changed":  func(f *remoteFixture) error { f.access(t); return nil },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f, w, _ := eventsFixture(t)
			session := f.eventsSession(t)
			subscribed, err := subscribe(t.Context(), session, w.url+"/hook", webhookSecret, "")
			if err != nil {
				t.Fatal(err)
			}
			w.next(t, "the verification handshake")
			stop := f.deliveries(t)
			f.deliver(t, "before the restart")
			w.next(t, "an event before the restart")
			stop()
			f.deliver(t, "while stopped")
			f.deliveries(t)
			if after := w.next(t, "an event after the restart"); after.subscription != subscribed.ID {
				t.Fatalf("after restart=%+v", after)
			}
			if err := change(f); err != nil {
				t.Fatal(err)
			}
			if _, live, err := f.r.LiveEventSubscription(subscribed.ID, time.Now()); err != nil || live {
				t.Fatalf("live=%v err=%v after the change", live, err)
			}
			if verified, err := f.r.EventEndpointVerified(f.scout.SessionID, w.url+"/hook", time.Time{}); err != nil || verified {
				t.Fatalf("the endpoint stayed verified for new credentials: %v %v", verified, err)
			}
			if name != "remove identity" {
				if _, _, err := f.r.StoreInboxMessage(registry.InboxMessage{Identity: f.scout.SessionID, UUID: "after", Message: "after"}); err != nil {
					t.Fatal(err)
				}
			}
			w.quiet(t, "after the credentials changed", 500*time.Millisecond)
		})
	}
}

package daemon

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
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
	"sync/atomic"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

func newWebhookSecret(t *testing.T) string {
	t.Helper()
	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	return "whsec_" + base64.StdEncoding.EncodeToString(key)
}

type webhookCall struct {
	id, timestamp, subscription string
	body                        map[string]any
	at                          time.Time
}

type webhookReceiver struct {
	t      *testing.T
	server *httptest.Server
	url    string
	calls  chan webhookCall
	mu     sync.Mutex
	secret string
	reply  func(call webhookCall) (int, string)
	header http.Header
	gate   chan struct{}
	gated  chan struct{}
}

func newWebhookReceiver(t *testing.T) *webhookReceiver {
	w := &webhookReceiver{t: t, calls: make(chan webhookCall, 64), secret: newWebhookSecret(t), gated: make(chan struct{}, eventSubscriptionLimit)}
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
	call := webhookCall{at: time.Now(), id: r.Header.Get("webhook-id"), timestamp: r.Header.Get("webhook-timestamp"), subscription: r.Header.Get("X-MCP-Subscription-Id")}
	w.mu.Lock()
	secret, reply, gate, header := w.secret, w.reply, w.gate, w.header
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
		w.gated <- struct{}{}
		<-gate
	}
	status, text := reply(call)
	w.calls <- call
	if call.body["type"] == nil {
		for key, values := range header {
			rw.Header()[key] = values
		}
	}
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
	subscribed, err := subscribe(t.Context(), session, callback, w.secret, "")
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
	again, err := subscribe(t.Context(), session, callback, w.secret, "")
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
	select {
	case <-w.gated:
	case <-time.After(10 * time.Second):
		t.Fatal("no event reached the gate")
	}
	f.deliver(t, "third")
	f.deliver(t, "fourth")
	close(gate)
	if held := w.next(t, "the event held at the gate"); held.body["data"].(map[string]any)["unread"] != float64(2) {
		t.Fatalf("held event=%v", held.body)
	}
	if burst := w.next(t, "the coalesced burst"); burst.body["data"].(map[string]any)["unread"] != float64(4) {
		t.Fatalf("burst event=%v", burst.body)
	}
	w.quiet(t, "after the burst was told", 300*time.Millisecond)

	history := f.eventHistory(t)
	if !strings.Contains(history, "events/list by @scout => listed inbox.unread") || !strings.Contains(history, "events/subscribe by @scout inbox.unread to "+w.url+"/hooks/kat => subscribed "+subscribed.ID+"; verification passed: HTTP 200") || !strings.Contains(history, "; verified within 24h0m0s") || !strings.Contains(history, "delivered") || strings.Contains(history, "receiver-credential") || strings.Contains(history, strings.TrimPrefix(w.secret, "whsec_")) || strings.Contains(history, "private") {
		t.Fatalf("history:\n%s", history)
	}

	for range 2 {
		if _, err := mcp.CallCustomMethod[*eventsUnsubscribeParams, *eventsUnsubscribeResult](t.Context(), session, "events/unsubscribe", &eventsUnsubscribeParams{Name: eventInboxUnread, Delivery: eventDelivery{Mode: "webhook", URL: callback}}); err != nil {
			t.Fatal(err)
		}
	}
	f.deliver(t, "after unsubscribing")
	w.quiet(t, "after unsubscribing", 500*time.Millisecond)
	if history := f.eventHistory(t); !strings.Contains(history, "events/unsubscribe by @scout inbox.unread to "+w.url+"/hooks/kat => unsubscribed "+subscribed.ID) || !strings.Contains(history, "=> no subscription "+subscribed.ID) {
		t.Fatalf("history:\n%s", history)
	}
}

func TestADeliveryThatFinishesAfterTheDueReadIsNotSentAgain(t *testing.T) {
	f, w, start := eventsFixture(t)
	session := f.eventsSession(t)
	if _, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, ""); err != nil {
		t.Fatal(err)
	}
	w.next(t, "the verification handshake")
	m := f.d.remoteMCP()
	var armed atomic.Bool
	listed, drain, drained := make(chan struct{}), make(chan struct{}), make(chan struct{})
	m.afterDueRead = func() {
		if !armed.CompareAndSwap(true, false) {
			return
		}
		close(listed)
		<-drain
		select {
		case <-m.eventWake:
		default:
		}
		close(drained)
		<-m.eventWake
	}
	w.mu.Lock()
	w.gate = make(chan struct{})
	gate := w.gate
	w.mu.Unlock()
	start()
	f.deliver(t, "one")
	select {
	case <-w.gated:
	case <-time.After(10 * time.Second):
		t.Fatal("no event reached the gate")
	}
	armed.Store(true)
	m.pokeEvents()
	select {
	case <-listed:
	case <-time.After(10 * time.Second):
		t.Fatal("the loop did not read the due events while the delivery was held")
	}
	close(drain)
	<-drained
	close(gate)
	if told := w.next(t, "the held event"); told.body["data"].(map[string]any)["unread"] != float64(1) {
		t.Fatalf("held event=%v", told.body)
	}
	w.quiet(t, "after the delivery finished behind a stale due read", 500*time.Millisecond)
}

func TestAWebhookIsRetriedWithItsEventIDAndA410EndsTheSubscription(t *testing.T) {
	f, w, start := eventsFixture(t)
	session := f.eventsSession(t)
	subscribed, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, "")
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
		_, live, err := f.r.LiveEventSubscription(subscribed.ID)
		return err == nil && !live
	})
	if history := f.eventHistory(t); !strings.Contains(history, "disabled") {
		t.Fatalf("history:\n%s", history)
	}
}

func TestAWebhookWaitsAsLongAsItsRetryAfterAsks(t *testing.T) {
	f, w, start := eventsFixture(t)
	session := f.eventsSession(t)
	if _, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, ""); err != nil {
		t.Fatal(err)
	}
	w.next(t, "the verification handshake")
	attempts := 0
	w.answer(func(webhookCall) (int, string) {
		attempts++
		if attempts == 1 {
			return http.StatusTooManyRequests, ""
		}
		return http.StatusOK, ""
	})
	w.mu.Lock()
	w.header = http.Header{"Retry-After": {"1"}}
	w.mu.Unlock()
	start()
	f.deliver(t, "one")
	first, retried := w.next(t, "the throttled attempt"), w.next(t, "the retry")
	if gap := retried.at.Sub(first.at); gap < 900*time.Millisecond || first.id != retried.id {
		t.Fatalf("retried after %s with id %s then %s, want at least the 1s asked", gap, first.id, retried.id)
	}

	for name, value := range map[string]string{"seconds": "3600", "HTTP date": time.Now().Add(time.Hour).UTC().Format(http.TimeFormat), "beyond int64": "99999999999999999999"} {
		w.answer(func(webhookCall) (int, string) { return http.StatusServiceUnavailable, "" })
		w.mu.Lock()
		w.header = http.Header{"Retry-After": {value}}
		w.mu.Unlock()
		f.deliver(t, "deferred by "+name)
		w.next(t, name+": the attempt that is told to wait an hour")
		w.quiet(t, name+": within the hour asked", 500*time.Millisecond)
		f.deliver(t, "more mail while deferred by "+name)
		w.quiet(t, name+": new mail while deferred", 500*time.Millisecond)
		if history := f.eventHistory(t); !strings.Contains(history, "deferred") || strings.Contains(history, "failed") {
			t.Fatalf("%s history:\n%s", name, history)
		}
		if _, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, ""); err != nil {
			t.Fatal(err)
		}
	}
}

func TestASubscriptionSuspendedForFailingIsRetriedOnATrialAndRecovers(t *testing.T) {
	f, w, start := eventsFixture(t)
	f.d.remoteMCP().eventRetries = []time.Duration{0}
	session := f.eventsSession(t)
	subscribed, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, "")
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
	sub, live, err := f.r.LiveEventSubscription(subscribed.ID)
	testkit.Eventually(t, "the trial clears the suspension", 5*time.Second, func() bool {
		sub, live, err = f.r.LiveEventSubscription(subscribed.ID)
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
		if _, err := subscribe(t.Context(), session, callback, w.secret, ""); err == nil {
			t.Fatalf("%s: subscribed", name)
		} else if code, _ := rpcError(t, err); code != jsonrpc.CodeInvalidParams {
			t.Fatalf("%s: code %d", name, code)
		}
	}
	if _, err := subscribe(t.Context(), session, w.url+"/hook", "whsec_c2hvcnQ=", ""); err == nil {
		t.Fatal("a short secret subscribed")
	}

	f.d.remoteMCP().webhookClient = newWebhookClient()
	_, err := subscribe(t.Context(), session, "https://localhost:"+strings.Split(w.server.URL, ":")[2]+"/hook", w.secret, "")
	if code, data := rpcError(t, err); code != codeCallbackEndpoint || data["reason"] != "blocked_address" {
		t.Fatalf("a name that resolves to loopback: code=%d data=%v", code, data)
	}
	w.quiet(t, "the guard refuses before connecting", 100*time.Millisecond)

	f.d.remoteMCP().webhookClient = w.client()
	w.answer(func(webhookCall) (int, string) { return http.StatusOK, `{"challenge":"not the one sent"}` })
	_, err = subscribe(t.Context(), session, w.url+"/hook", w.secret, "")
	if code, data := rpcError(t, err); code != codeCallbackEndpoint || data["reason"] != "challenge_failed" {
		t.Fatalf("a wrong challenge: code=%d data=%v", code, data)
	}
	w.answer(func(webhookCall) (int, string) { return http.StatusNotFound, "" })
	if _, err := subscribe(t.Context(), session, w.url+"/missing?token=receiver-credential", w.secret, ""); err == nil {
		t.Fatal("an endpoint that answered 404 subscribed")
	}
	history := f.eventHistory(t)
	for _, line := range []string{
		"events/subscribe by @scout inbox.unread to https://10.0.0.7/hook => refused: invalid params: delivery.url must reach a public address",
		"events/subscribe by @scout inbox.unread to https://example.com/hook => refused: invalid params: delivery.url must not carry credentials",
		"=> refused: invalid params: delivery.secret must be whsec_",
		"/hook => refused: verification failed: blocked_address",
		w.url + "/hook => refused: verification failed: challenge_failed (HTTP 200)",
		w.url + "/missing => refused: verification failed: http_4xx (HTTP 404)",
	} {
		if !strings.Contains(history, line) {
			t.Fatalf("no %q in history:\n%s", line, history)
		}
	}
	if strings.Contains(history, "pass@") || strings.Contains(history, "receiver-credential") || strings.Contains(history, strings.TrimPrefix(w.secret, "whsec_")) {
		t.Fatalf("history kept a credential:\n%s", history)
	}
}

func TestEventSubscriptionsAreBoundedAndNeverExpireWhateverTTLIsAsked(t *testing.T) {
	f, w, _ := eventsFixture(t)
	session := f.eventsSession(t)
	week, err := subscribe(t.Context(), session, w.url+"/week", w.secret, "604800000")
	if err != nil || week.RefreshBefore != nil {
		t.Fatalf("ttlMs of a week: %+v err=%v, want no refreshBefore", week, err)
	}
	if _, live, err := f.r.LiveEventSubscription(week.ID); err != nil || !live {
		t.Fatalf("the week-long subscription: live=%v err=%v", live, err)
	}
	forever, err := subscribe(t.Context(), session, w.url+"/forever", w.secret, "null")
	if err != nil || forever.RefreshBefore != nil {
		t.Fatalf("ttlMs null: %+v err=%v", forever, err)
	}
	_, err = subscribe(t.Context(), session, w.url+"/malformed", w.secret, "-1")
	if code, _ := rpcError(t, err); code != jsonrpc.CodeInvalidParams {
		t.Fatalf("ttlMs -1: code=%d err=%v", code, err)
	}
	for i := 2; i < eventSubscriptionLimit; i++ {
		if _, err := subscribe(t.Context(), session, fmt.Sprintf("%s/hook-%d", w.url, i), w.secret, ""); err != nil {
			t.Fatal(err)
		}
	}
	_, err = subscribe(t.Context(), session, w.url+"/one-too-many", w.secret, "")
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
			subscribed, err := subscribe(t.Context(), session, w.url+"/hook", w.secret, "")
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
			if _, live, err := f.r.LiveEventSubscription(subscribed.ID); err != nil || live {
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

func TestStoppingDeliveriesReturnsWhileEveryEndpointHangs(t *testing.T) {
	f, w, _ := eventsFixture(t)
	session := f.eventsSession(t)
	for i := range eventSubscriptionLimit {
		if _, err := subscribe(t.Context(), session, fmt.Sprintf("%s/hook-%d", w.url, i), w.secret, ""); err != nil {
			t.Fatal(err)
		}
		w.next(t, "a verification handshake")
	}
	w.mu.Lock()
	w.gate = make(chan struct{})
	gate := w.gate
	w.mu.Unlock()
	t.Cleanup(func() { close(gate) })
	stop := f.deliveries(t)
	f.deliver(t, "one")
	for range eventDeliveries {
		select {
		case <-w.gated:
		case <-time.After(10 * time.Second):
			t.Fatal("deliveries did not reach the hanging endpoints")
		}
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stopping the delivery loop hung behind deliveries in flight")
	}
}

type rawEventParams struct {
	mcp.ParamsBase
	body string
}

func (p *rawEventParams) MarshalJSON() ([]byte, error) { return []byte(p.body), nil }

func TestEveryEventCallIsRecordedEvenWhenItsParamsAreMalformedAndNoCredentialIsKept(t *testing.T) {
	f, w, _ := eventsFixture(t)
	session := f.eventsSession(t)
	for _, method := range []string{"events/list", "events/subscribe", "events/unsubscribe"} {
		_, err := mcp.CallCustomMethod[*rawEventParams, *eventsUnsubscribeResult](t.Context(), session, method, &rawEventParams{body: `[]`})
		if code, _ := rpcError(t, err); code != jsonrpc.CodeInvalidParams {
			t.Fatalf("%s with array params: code %d", method, code)
		}
		if history := f.eventHistory(t); !strings.Contains(history, method+" by @scout => refused: invalid params: they do not decode") {
			t.Fatalf("%s with array params left no record:\n%s", method, history)
		}
	}
	missing := map[string]string{"events/list": "=> listed inbox.unread", "events/subscribe": "an unknown event => refused: invalid params: no such event", "events/unsubscribe": "an unknown event => refused: invalid params: no such event"}
	for method, line := range missing {
		for _, body := range []string{"", "null"} {
			before := strings.Count(f.eventHistory(t), method+" by @scout "+line)
			var params *rawEventParams
			if body != "" {
				params = &rawEventParams{body: body}
			}
			var err error
			if method == "events/list" {
				_, err = mcp.CallCustomMethod[*rawEventParams, *eventsListResult](t.Context(), session, method, params)
			} else {
				_, err = mcp.CallCustomMethod[*rawEventParams, *eventsUnsubscribeResult](t.Context(), session, method, params)
			}
			if (method == "events/list") != (err == nil) {
				t.Fatalf("%s with params %q: err=%v", method, body, err)
			}
			if after := strings.Count(f.eventHistory(t), method+" by @scout "+line); after != before+1 {
				t.Fatalf("%s with params %q: %d new records:\n%s", method, body, after-before, f.eventHistory(t))
			}
		}
	}
	leaky := []string{w.secret, "https://user:pass@example.com/?token=receiver-credential#frag"}
	for _, name := range leaky {
		if _, err := mcp.CallCustomMethod[*eventsSubscribeParams, *eventsSubscribeResult](t.Context(), session, "events/subscribe", &eventsSubscribeParams{Name: name, Delivery: eventDelivery{Mode: "webhook", URL: w.url + "/hook", Secret: w.secret}}); err == nil {
			t.Fatalf("an unknown event %q subscribed", name)
		}
		if _, err := mcp.CallCustomMethod[*eventsUnsubscribeParams, *eventsUnsubscribeResult](t.Context(), session, "events/unsubscribe", &eventsUnsubscribeParams{Name: name, Delivery: eventDelivery{Mode: "webhook", URL: w.url + "/hook"}}); err == nil {
			t.Fatalf("an unknown event %q unsubscribed", name)
		}
	}
	history := f.eventHistory(t)
	if strings.Count(history, "an unknown event to "+w.url+"/hook => refused: invalid params: no such event") != 4 {
		t.Fatalf("history:\n%s", history)
	}
	for _, kept := range []string{strings.TrimPrefix(w.secret, "whsec_"), "pass@", "receiver-credential", "frag"} {
		if strings.Contains(history, kept) || strings.Contains(f.log.String(), kept) {
			t.Fatalf("%q was kept:\n%s\n%s", kept, history, f.log.String())
		}
	}
}

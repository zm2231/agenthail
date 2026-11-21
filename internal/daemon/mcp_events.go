package daemon

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"code.dny.dev/ssrf"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
)

const (
	eventInboxUnread       = "inbox.unread"
	eventSubscriptionLimit = 8
	eventMinTTL            = 5 * time.Minute
	eventVerifiedFor       = 24 * time.Hour
	eventSecretGrace       = 10 * time.Minute
	eventRequestTimeout    = 10 * time.Second
	eventSuspendAfter      = 3
	eventTrialEvery        = 6 * time.Hour
	eventDeliveries        = 4
	eventURLLimit          = 2048
	eventResponseLimit     = 64 << 10
	eventSweep             = 5 * time.Second
	eventRetryAfterInRun   = 10 * time.Minute

	codeCallbackEndpoint = -32015
	historyMCPEvent      = "mcp-event"
)

var eventRetrySchedule = []time.Duration{0, 15 * time.Second, time.Minute, 4 * time.Minute, 10 * time.Minute}

type eventPrincipalKey struct{}

type eventPrincipal struct {
	identity, name, tokenHash string
	epoch                     int64
}

type eventsListParams struct {
	mcp.ParamsBase
	Cursor *string `json:"cursor,omitempty"`
}

type eventDescriptor struct {
	Name          string         `json:"name"`
	Description   string         `json:"description"`
	Delivery      []string       `json:"delivery"`
	InputSchema   map[string]any `json:"inputSchema"`
	PayloadSchema map[string]any `json:"payloadSchema"`
}

type eventsListResult struct {
	mcp.ResultBase
	Events []eventDescriptor `json:"events"`
}

type eventDelivery struct {
	Mode   string `json:"mode"`
	URL    string `json:"url"`
	Secret string `json:"secret,omitempty"`
}

type eventsSubscribeParams struct {
	mcp.ParamsBase
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Delivery  eventDelivery   `json:"delivery"`
	Cursor    *string         `json:"cursor,omitempty"`
	TTLMs     json.RawMessage `json:"ttlMs,omitempty"`
}

type eventsSubscribeResult struct {
	mcp.ResultBase
	ID            string  `json:"id"`
	RefreshBefore *string `json:"refreshBefore"`
	Cursor        *string `json:"cursor"`
	Truncated     bool    `json:"truncated"`
}

type eventsUnsubscribeParams struct {
	mcp.ParamsBase
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments,omitempty"`
	Delivery  eventDelivery   `json:"delivery"`
}

type eventsUnsubscribeResult struct {
	mcp.ResultBase
}

var inboxUnreadEvent = eventDescriptor{
	Name:          eventInboxUnread,
	Description:   "Your inbox has unread messages you were not yet told of. Carries only the count; read them with agenthail_read.",
	Delivery:      []string{"webhook"},
	InputSchema:   map[string]any{"type": "object", "properties": map[string]any{}, "additionalProperties": false},
	PayloadSchema: map[string]any{"type": "object", "properties": map[string]any{"unread": map[string]any{"type": "integer", "minimum": 1}}, "required": []string{"unread"}, "additionalProperties": false},
}

func (m *remoteMCP) registerEvents() {
	must := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	must(mcp.AddReceivingCustomMethod(m.server, "events/list", m.listEvents))
	must(mcp.AddReceivingCustomMethod(m.server, "events/subscribe", m.subscribeEvent))
	must(mcp.AddReceivingCustomMethod(m.server, "events/unsubscribe", m.unsubscribeEvent))
	m.server.AddReceivingMiddleware(m.eventsMiddleware)
}

// Custom method handlers get no RequestExtra, so the caller's identity rides in the context.
func (m *remoteMCP) eventsMiddleware(next mcp.MethodHandler) mcp.MethodHandler {
	return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
		switch method {
		case "initialize", "server/discover":
			result, err := next(ctx, method, req)
			if err != nil || result == nil {
				return result, err
			}
			return eventsCapable{result}, nil
		case "events/list", "events/subscribe", "events/unsubscribe":
			if principal, ok := principalOf(req.GetExtra()); ok {
				ctx = context.WithValue(ctx, eventPrincipalKey{}, principal)
			}
		}
		return next(ctx, method, req)
	}
}

func principalOf(extra *mcp.RequestExtra) (eventPrincipal, bool) {
	name, info := remoteName(extra)
	if name == "" {
		return eventPrincipal{}, false
	}
	hash, _ := info.Extra[tokenInfoTokenHash].(string)
	epoch, _ := info.Extra[tokenInfoEpoch].(int64)
	return eventPrincipal{identity: info.UserID, name: name, tokenHash: hash, epoch: epoch}, hash != ""
}

func eventCaller(ctx context.Context) (eventPrincipal, error) {
	principal, ok := ctx.Value(eventPrincipalKey{}).(eventPrincipal)
	if !ok {
		return eventPrincipal{}, &jsonrpc.Error{Code: jsonrpc.CodeInvalidRequest, Message: "events are served only to a remote agent identity"}
	}
	return principal, nil
}

// The SDK's capabilities have no events field.
type eventsCapable struct{ mcp.Result }

func (e eventsCapable) MarshalJSON() ([]byte, error) {
	encoded, err := json.Marshal(e.Result)
	if err != nil {
		return nil, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &fields); err != nil {
		return nil, err
	}
	capabilities := map[string]json.RawMessage{}
	if raw, ok := fields["capabilities"]; ok && string(raw) != "null" {
		if err := json.Unmarshal(raw, &capabilities); err != nil {
			return nil, err
		}
	}
	capabilities["events"] = json.RawMessage(`{}`)
	if fields["capabilities"], err = json.Marshal(capabilities); err != nil {
		return nil, err
	}
	return json.Marshal(fields)
}

func invalidEventParams(format string, args ...any) error {
	return &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: fmt.Sprintf(format, args...)}
}

func callbackError(reason string) error {
	data, _ := json.Marshal(map[string]string{"reason": reason})
	return &jsonrpc.Error{Code: codeCallbackEndpoint, Message: "the callback endpoint could not be verified: " + reason, Data: data}
}

func (m *remoteMCP) listEvents(ctx context.Context, _ *mcp.ServerSession, _ *eventsListParams) (*eventsListResult, error) {
	if _, err := eventCaller(ctx); err != nil {
		return nil, err
	}
	return &eventsListResult{Events: []eventDescriptor{inboxUnreadEvent}}, nil
}

func eventArguments(name string, raw json.RawMessage) (string, error) {
	if name != eventInboxUnread {
		return "", invalidEventParams("no event is named %q; events/list names %s", name, eventInboxUnread)
	}
	if len(raw) == 0 || string(raw) == "null" {
		return "{}", nil
	}
	var args map[string]json.RawMessage
	if err := json.Unmarshal(raw, &args); err != nil || len(args) != 0 {
		return "", invalidEventParams("%s takes no arguments", eventInboxUnread)
	}
	return "{}", nil
}

func eventCallbackURL(delivery eventDelivery) (string, error) {
	if delivery.Mode != "webhook" {
		return "", invalidEventParams("delivery.mode must be webhook")
	}
	if len(delivery.URL) > eventURLLimit {
		return "", invalidEventParams("delivery.url is longer than %d bytes", eventURLLimit)
	}
	parsed, err := url.Parse(delivery.URL)
	switch {
	case err != nil:
		return "", invalidEventParams("delivery.url does not parse")
	case parsed.Scheme != "https" || parsed.Host == "" || parsed.Opaque != "":
		return "", invalidEventParams("delivery.url must be an https URL")
	case parsed.User != nil:
		return "", invalidEventParams("delivery.url must not carry credentials")
	case parsed.Fragment != "" || strings.Contains(delivery.URL, "#"):
		return "", invalidEventParams("delivery.url must not have a fragment")
	}
	if addr, err := netip.ParseAddr(strings.Trim(parsed.Hostname(), "[]")); err == nil {
		if err := webhookGuard.SafeAddr(addr); err != nil {
			return "", invalidEventParams("delivery.url must reach a public address")
		}
	}
	return delivery.URL, nil
}

func webhookKey(secret string) ([]byte, bool) {
	encoded, ok := strings.CutPrefix(secret, "whsec_")
	if !ok {
		return nil, false
	}
	key, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		if key, err = base64.RawStdEncoding.DecodeString(encoded); err != nil {
			return nil, false
		}
	}
	return key, len(key) >= 24 && len(key) <= 64
}

func eventTTL(raw json.RawMessage, now time.Time) (time.Time, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return time.Time{}, nil
	}
	var ms int64
	if err := json.Unmarshal(raw, &ms); err != nil || ms < 0 {
		return time.Time{}, invalidEventParams("ttlMs must be a non-negative integer or null")
	}
	return now.Add(max(time.Duration(ms)*time.Millisecond, eventMinTTL)), nil
}

func eventSubscriptionID(identity, callback, name, args string) string {
	sum := sha256.Sum256([]byte(identity + "\x00" + callback + "\x00" + name + "\x00" + args))
	return "sub_" + hex.EncodeToString(sum[:16])
}

func (m *remoteMCP) subscribeEvent(ctx context.Context, _ *mcp.ServerSession, params *eventsSubscribeParams) (*eventsSubscribeResult, error) {
	principal, err := eventCaller(ctx)
	if err != nil {
		return nil, err
	}
	args, err := eventArguments(params.Name, params.Arguments)
	if err != nil {
		return nil, err
	}
	callback, err := eventCallbackURL(params.Delivery)
	if err != nil {
		return nil, err
	}
	if _, ok := webhookKey(params.Delivery.Secret); !ok {
		return nil, invalidEventParams("delivery.secret must be whsec_ followed by base64 of 24 to 64 bytes")
	}
	now := time.Now()
	refreshBefore, err := eventTTL(params.TTLMs, now)
	if err != nil {
		return nil, err
	}
	id := eventSubscriptionID(principal.identity, callback, params.Name, args)
	verified, err := m.d.Registry.EventEndpointVerified(principal.identity, callback, now.Add(-eventVerifiedFor))
	if err != nil {
		return nil, err
	}
	if !verified {
		if reason := m.verifyCallback(ctx, id, callback, params.Delivery.Secret); reason != "" {
			m.recordEvent(principal.identity, callback, "refused: "+reason)
			return nil, callbackError(reason)
		}
	}
	sub, err := m.d.Registry.SubscribeEvent(registry.EventSubscribe{ID: id, IdentityID: principal.identity, TokenHash: principal.tokenHash, Epoch: principal.epoch, Name: params.Name, Args: args, URL: callback, Secret: params.Delivery.Secret, RefreshBefore: refreshBefore, Verified: !verified, Limit: eventSubscriptionLimit, SecretGrace: eventSecretGrace})
	if errors.Is(err, registry.ErrEventSubscriptionLimit) {
		data, _ := json.Marshal(map[string]any{"limit": "subscriptions", "max": eventSubscriptionLimit})
		return nil, &jsonrpc.Error{Code: jsonrpc.CodeInvalidParams, Message: err.Error(), Data: data}
	}
	if err != nil {
		return nil, err
	}
	m.recordEvent(principal.identity, callback, "subscribed")
	m.pokeEvents()
	result := &eventsSubscribeResult{ID: sub.ID}
	if !sub.RefreshBefore.IsZero() {
		at := sub.RefreshBefore.UTC().Format(time.RFC3339)
		result.RefreshBefore = &at
	}
	return result, nil
}

func (m *remoteMCP) unsubscribeEvent(ctx context.Context, _ *mcp.ServerSession, params *eventsUnsubscribeParams) (*eventsUnsubscribeResult, error) {
	principal, err := eventCaller(ctx)
	if err != nil {
		return nil, err
	}
	args, err := eventArguments(params.Name, params.Arguments)
	if err != nil {
		return nil, err
	}
	callback, err := eventCallbackURL(params.Delivery)
	if err != nil {
		return nil, err
	}
	removed, err := m.d.Registry.UnsubscribeEvent(principal.identity, eventSubscriptionID(principal.identity, callback, params.Name, args))
	if err != nil {
		return nil, err
	}
	if removed {
		m.recordEvent(principal.identity, callback, "unsubscribed")
	}
	return &eventsUnsubscribeResult{}, nil
}

// A query string can carry the receiver's own credentials, so history keeps only where it points.
func eventURLForHistory(callback string) string {
	parsed, err := url.Parse(callback)
	if err != nil {
		return "an unparseable URL"
	}
	return parsed.Scheme + "://" + parsed.Host + parsed.Path
}

func (m *remoteMCP) recordEvent(identity, callback, result string) {
	if err := m.d.Registry.RecordHistory(registry.HistoryEntry{Kind: historyMCPEvent, SourceSessionID: identity, Message: eventInboxUnread + " to " + eventURLForHistory(callback), Result: result}); err != nil {
		m.d.logRuntimeError("record an mcp event", err)
	}
}

var webhookGuard = ssrf.New(ssrf.WithAnyPort())

func newWebhookClient() *http.Client {
	dialer := &net.Dialer{Timeout: eventRequestTimeout, Control: webhookGuard.Safe}
	return &http.Client{
		Timeout: eventRequestTimeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           dialer.DialContext,
			TLSClientConfig:       &tls.Config{MinVersion: tls.VersionTLS12},
			TLSHandshakeTimeout:   eventRequestTimeout,
			ResponseHeaderTimeout: eventRequestTimeout,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          eventDeliveries,
			IdleConnTimeout:       time.Minute,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

func webhookSignature(id string, timestamp int64, body []byte, secrets []string) string {
	signatures := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		key, ok := webhookKey(secret)
		if !ok {
			continue
		}
		mac := hmac.New(sha256.New, key)
		fmt.Fprintf(mac, "%s.%d.", id, timestamp)
		mac.Write(body)
		signatures = append(signatures, "v1,"+base64.StdEncoding.EncodeToString(mac.Sum(nil)))
	}
	return strings.Join(signatures, " ")
}

type webhookReply struct {
	status     int
	body       []byte
	retryAfter time.Duration
	reason     string
}

func (m *remoteMCP) postWebhook(ctx context.Context, subscription, callback, id string, body []byte, secrets []string) webhookReply {
	ctx, cancel := context.WithTimeout(ctx, eventRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, callback, bytes.NewReader(body))
	if err != nil {
		return webhookReply{reason: "connection_refused"}
	}
	timestamp := time.Now().Unix()
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("webhook-id", id)
	req.Header.Set("webhook-timestamp", strconv.FormatInt(timestamp, 10))
	req.Header.Set("webhook-signature", webhookSignature(id, timestamp, body, secrets))
	req.Header.Set("X-MCP-Subscription-Id", subscription)
	resp, err := m.webhookClient.Do(req)
	if err != nil {
		return webhookReply{reason: transportReason(err)}
	}
	defer resp.Body.Close()
	reply := webhookReply{status: resp.StatusCode}
	reply.body, _ = io.ReadAll(io.LimitReader(resp.Body, eventResponseLimit))
	reply.retryAfter = retryAfter(resp.Header.Get("Retry-After"), time.Now())
	switch {
	case resp.StatusCode >= 500:
		reply.reason = "http_5xx"
	case resp.StatusCode >= 300:
		reply.reason = "http_4xx"
	}
	return reply
}

func retryAfter(value string, now time.Time) time.Duration {
	if digits := strings.TrimSpace(value); digits != "" && strings.Trim(digits, "0123456789") == "" {
		const most = math.MaxInt64 / int64(time.Second)
		seconds, err := strconv.ParseInt(digits, 10, 64)
		if err != nil || seconds > most {
			seconds = most
		}
		return time.Duration(seconds) * time.Second
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(at.Sub(now), 0)
	}
	return 0
}

func transportReason(err error) string {
	var netErr net.Error
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError
	switch {
	case errors.Is(err, ssrf.ErrProhibitedIP), errors.Is(err, ssrf.ErrProhibitedNetwork), errors.Is(err, ssrf.ErrProhibitedPort):
		return "blocked_address"
	case errors.As(err, &certErr), errors.As(err, &recordErr):
		return "tls_error"
	case errors.Is(err, context.DeadlineExceeded), errors.As(err, &netErr) && netErr.Timeout():
		return "timeout"
	}
	return "connection_refused"
}

// verifyCallback returns the reason the endpoint failed the handshake, or "" once it echoed the challenge.
func (m *remoteMCP) verifyCallback(ctx context.Context, subscription, callback, secret string) string {
	nonce := make([]byte, 32)
	idBytes := make([]byte, 12)
	if _, err := rand.Read(nonce); err != nil {
		return "connection_refused"
	}
	if _, err := rand.Read(idBytes); err != nil {
		return "connection_refused"
	}
	challenge := base64.RawURLEncoding.EncodeToString(nonce)
	body, err := json.Marshal(map[string]string{"type": "verification", "challenge": challenge})
	if err != nil {
		return "connection_refused"
	}
	reply := m.postWebhook(ctx, subscription, callback, "msg_verification_"+hex.EncodeToString(idBytes), body, []string{secret})
	if reply.reason != "" {
		return reply.reason
	}
	if reply.status < 200 || reply.status > 299 {
		return "http_4xx"
	}
	var echoed struct {
		Challenge string `json:"challenge"`
	}
	if json.Unmarshal(reply.body, &echoed) != nil || subtle.ConstantTimeCompare([]byte(echoed.Challenge), []byte(challenge)) != 1 {
		return "challenge_failed"
	}
	return ""
}

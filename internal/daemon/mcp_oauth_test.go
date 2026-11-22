package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"github.com/zm2231/agenthail/internal/mcpoauth"
	"github.com/zm2231/agenthail/internal/registry"
)

const oauthTestCallback = "http://localhost:9/callback"

type oauthFixture struct {
	*remoteFixture
	issuer  string
	client  *http.Client
	browser *http.Client
}

func newOAuthFixture(t *testing.T) *oauthFixture {
	t.Helper()
	f := &oauthFixture{remoteFixture: newRemoteFixture(t, "")}
	u, err := url.Parse(f.server.URL)
	if err != nil {
		t.Fatal(err)
	}
	f.issuer = "http://localhost:" + u.Port()
	if err := f.r.SetOAuthIssuer(f.issuer); err != nil {
		t.Fatal(err)
	}
	tunnel := headerTransport{"X-Forwarded-For": "203.0.113.9"}
	f.client = &http.Client{Transport: tunnel}
	f.browser = &http.Client{Transport: tunnel, CheckRedirect: func(r *http.Request, _ []*http.Request) error {
		if r.URL.Host == "localhost:9" {
			return http.ErrUseLastResponse
		}
		return nil
	}}
	return f
}

func (f *oauthFixture) get(t *testing.T, target string) (*http.Response, string) {
	t.Helper()
	response, err := f.browser.Get(target)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	return response, string(body)
}

var matchCodeOnPage = regexp.MustCompile(`class="code">(\d{6})<`)

func (f *oauthFixture) authorize(t *testing.T, authURL, identity, choice string) *url.URL {
	t.Helper()
	if response, body := f.get(t, authURL); response.StatusCode != http.StatusOK || !strings.Contains(body, "wants to connect to Agenthail") {
		t.Fatalf("authorize page: status=%d body=%s", response.StatusCode, body)
	}
	parsed, err := url.Parse(authURL)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"identity": {identity}}
	for key, value := range mcpoauth.HiddenParams(parsed.Query()) {
		form.Set(key, value)
	}
	response, err := f.browser.PostForm(f.issuer+mcpoauth.AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	waitingURL := response.Request.URL.String()
	code := matchCodeOnPage.FindStringSubmatch(string(page))
	if response.StatusCode != http.StatusOK || code == nil {
		t.Fatalf("waiting page: status=%d body=%s", response.StatusCode, page)
	}
	if choice != "" {
		ask := f.openOAuthAsk(t)
		if !strings.Contains(ask.Questions[0].Question, code[1]) {
			t.Fatalf("the ask does not show the page's code %s: %q", code[1], ask.Questions[0].Question)
		}
		if answer := runDashboardAction(t, f.d, fmt.Sprintf(`{"action":"respond","sessionId":%q,"requestId":%q,"decision":{"choices":[%q]}}`, ask.SessionID, ask.ID, choice)); answer.Code != http.StatusOK {
			t.Fatalf("answer: %d %s", answer.Code, answer.Body)
		}
	}
	final, body := f.get(t, waitingURL)
	if final.StatusCode != http.StatusFound {
		t.Fatalf("after the answer: status=%d body=%s", final.StatusCode, body)
	}
	location, err := url.Parse(final.Header.Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location
}

func (f *oauthFixture) openOAuthAsk(t *testing.T) registry.Ask {
	t.Helper()
	open, err := f.r.OpenAsks()
	if err != nil {
		t.Fatal(err)
	}
	for _, ask := range open {
		if ask.Origin == registry.AskOriginOAuthGrant {
			return ask
		}
	}
	t.Fatal("no OAuth approval was asked")
	return registry.Ask{}
}

func (f *oauthFixture) handler(t *testing.T, choice string) *auth.AuthorizationCodeHandler {
	t.Helper()
	handler, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{Metadata: &oauthex.ClientRegistrationMetadata{ClientName: "Kat", RedirectURIs: []string{oauthTestCallback}}},
		RedirectURL:                     oauthTestCallback,
		Client:                          f.client,
		AuthorizationCodeFetcher: func(_ context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			back := f.authorize(t, args.URL, "@scout", choice).Query()
			if back.Get("error") != "" {
				return nil, errors.New(back.Get("error"))
			}
			return &auth.AuthorizationResult{Code: back.Get("code"), State: back.Get("state"), Iss: back.Get("iss")}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return handler
}

func (f *oauthFixture) connect(t *testing.T, handler auth.OAuthHandler) (*mcp.ClientSession, error) {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: f.issuer + mcpoauth.ResourcePath, HTTPClient: f.client, OAuthHandler: handler, MaxRetries: -1}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "kat", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err == nil {
		t.Cleanup(func() { session.Close() })
	}
	return session, err
}

func TestAURLOnlyClientConnectsAsTheIdentityThePersonApproves(t *testing.T) {
	f := newOAuthFixture(t)
	f.deliver(t, "hello from the builder")
	handler := f.handler(t, oauthApprove)
	session, err := f.connect(t, handler)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "agenthail_read", Arguments: map[string]any{}})
	if err != nil || result.IsError {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if text := fmt.Sprint(result.Content[0]); !strings.Contains(text, "hello from the builder") {
		t.Fatalf("the client did not read @scout's inbox: %s", text)
	}
	grants, err := f.r.OAuthGrants()
	if err != nil || len(grants) != 1 || grants[0].Identity != "scout" || grants[0].ClientName != "Kat" {
		t.Fatalf("grants=%+v err=%v", grants, err)
	}
	history := f.history(t)
	for _, want := range []string{"mcp-oauth: register Kat", "asked the person", "approved by", "mcp-oauth: issue grant " + grants[0].ID, "mcp-remote: agenthail_read via oauth Kat"} {
		if !strings.Contains(history, want) {
			t.Fatalf("history lacks %q:\n%s", want, history)
		}
	}
	token, err := handler.TokenSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := token.Token()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.RevokeOAuthGrant(grants[0].ID); err != nil {
		t.Fatal(err)
	}
	status, challenge := f.probe(t, issued.AccessToken)
	if status != http.StatusUnauthorized || !strings.Contains(challenge, `resource_metadata="`+f.issuer+`/.well-known/oauth-protected-resource/mcp"`) {
		t.Fatalf("a revoked token: status=%d challenge=%q", status, challenge)
	}
}

func TestWithAccessSetAnAppStillConnectsOverOAuthAndARemoteTokenStillNeedsAccess(t *testing.T) {
	f := newOAuthFixture(t)
	f.access(t)
	f.deliver(t, "past Access")
	session, err := f.connect(t, f.handler(t, oauthApprove))
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "agenthail_read", Arguments: map[string]any{}})
	if err != nil || result.IsError || !strings.Contains(fmt.Sprint(result.Content[0]), "past Access") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if status, _ := f.probe(t, f.remoteFixture.token); status != http.StatusUnauthorized {
		t.Fatalf("an ahr_ token without an Access credential got %d", status)
	}
}

func (f *oauthFixture) history(t *testing.T) string {
	t.Helper()
	entries, err := f.r.ListHistory(200, "")
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, entry := range entries {
		if entry.Kind == historyMCPOAuth || entry.Kind == "mcp-remote" {
			lines = append(lines, entry.Kind+": "+entry.Message+" => "+entry.Result)
		}
	}
	return strings.Join(lines, "\n")
}

func (f *oauthFixture) probe(t *testing.T, token string) (int, string) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, f.issuer+mcpoauth.ResourcePath, strings.NewReader(mcpInitialize))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response, err := f.client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	return response.StatusCode, response.Header.Get("WWW-Authenticate")
}

func (f *oauthFixture) token(t *testing.T, form url.Values) (int, map[string]any) {
	t.Helper()
	response, err := f.client.PostForm(f.issuer+mcpoauth.TokenPath, form)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body
}

func TestAProbeWithoutATokenFindsTheAuthorizationServer(t *testing.T) {
	f := newOAuthFixture(t)
	status, challenge := f.probe(t, "")
	metadata := f.issuer + "/.well-known/oauth-protected-resource/mcp"
	if status != http.StatusUnauthorized || challenge != `Bearer realm="agenthail", resource_metadata="`+metadata+`"` {
		t.Fatalf("status=%d challenge=%q", status, challenge)
	}
	resource, err := oauthex.GetProtectedResourceMetadata(t.Context(), metadata, f.issuer+"/mcp", f.client)
	if err != nil || len(resource.AuthorizationServers) != 1 || resource.AuthorizationServers[0] != f.issuer {
		t.Fatalf("resource=%+v err=%v", resource, err)
	}
	server, err := oauthex.GetAuthServerMeta(t.Context(), f.issuer+"/.well-known/oauth-authorization-server", f.issuer, f.client)
	if err != nil || server.RegistrationEndpoint != f.issuer+"/oauth/register" {
		t.Fatalf("server=%+v err=%v", server, err)
	}
	if err := f.r.ClearOAuthIssuer(); err != nil {
		t.Fatal(err)
	}
	if status, challenge := f.probe(t, ""); status != http.StatusUnauthorized || challenge != `Bearer realm="agenthail"` {
		t.Fatalf("with OAuth off: status=%d challenge=%q", status, challenge)
	}
	if response, _ := f.get(t, metadata); response.StatusCode != http.StatusNotFound {
		t.Fatalf("with OAuth off the metadata answered %d", response.StatusCode)
	}
}

func TestAClientThePersonDeclinesGetsNoToken(t *testing.T) {
	f := newOAuthFixture(t)
	if _, err := f.connect(t, f.handler(t, oauthDecline)); err == nil || !strings.Contains(err.Error(), "access_denied") {
		t.Fatalf("a declined client connected: %v", err)
	}
	if grants, err := f.r.OAuthGrants(); err != nil || len(grants) != 0 {
		t.Fatalf("grants=%+v err=%v", grants, err)
	}
	if history := f.history(t); !strings.Contains(history, "=> declined by") {
		t.Fatalf("the decline is not recorded:\n%s", history)
	}
}

func TestAnUnknownIdentityGetsTheSamePageAndNoAsk(t *testing.T) {
	f := newOAuthFixture(t)
	client, err := f.r.RegisterOAuthClient("Kat", []string{oauthTestCallback}, 64, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	form := url.Values{"identity": {"nobody"}, "response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {oauthTestCallback}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}}
	response, err := f.browser.PostForm(f.issuer+mcpoauth.AuthorizePath, form)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || matchCodeOnPage.FindStringSubmatch(string(page)) == nil {
		t.Fatalf("status=%d page=%s", response.StatusCode, page)
	}
	if open, err := f.r.OpenAsks(); err != nil || len(open) != 0 {
		t.Fatalf("an unknown identity raised asks: %+v err=%v", open, err)
	}
}

func TestARefreshRotatesTheTokensAndASpentOneRevokesTheGrant(t *testing.T) {
	f := newOAuthFixture(t)
	handler := f.handler(t, oauthApprove)
	if _, err := f.connect(t, handler); err != nil {
		t.Fatal(err)
	}
	source, err := handler.TokenSource(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	first, err := source.Token()
	if err != nil {
		t.Fatal(err)
	}
	grants, err := f.r.OAuthGrants()
	if err != nil || len(grants) != 1 {
		t.Fatalf("grants=%+v err=%v", grants, err)
	}
	refresh := url.Values{"grant_type": {"refresh_token"}, "client_id": {grants[0].ClientID}, "refresh_token": {first.RefreshToken}}
	status, body := f.token(t, refresh)
	if status != http.StatusOK || body["refresh_token"] == first.RefreshToken || body["access_token"] == nil {
		t.Fatalf("refresh: status=%d body=%v", status, body)
	}
	if status, _ := f.probe(t, body["access_token"].(string)); status != http.StatusOK {
		t.Fatalf("the refreshed access token got %d", status)
	}
	if status, body := f.token(t, refresh); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("a spent refresh token: status=%d body=%v", status, body)
	}
	if status, _ := f.probe(t, body["access_token"].(string)); status != http.StatusUnauthorized {
		t.Fatalf("the grant outlived a spent refresh token: %d", status)
	}
	if history := f.history(t); !strings.Contains(history, "a spent refresh token was presented") {
		t.Fatalf("the revocation is not recorded:\n%s", history)
	}
}

type approvedCode struct {
	clientID string
	code     string
	verifier string
}

func (f *oauthFixture) approvedCode(t *testing.T, sendRedirect bool) approvedCode {
	t.Helper()
	client, err := f.r.RegisterOAuthClient("Kat", []string{oauthTestCallback}, 64, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	verifier := strings.Repeat("v", 50)
	sum := sha256.Sum256([]byte(verifier))
	query := url.Values{"response_type": {"code"}, "client_id": {client.ID}, "state": {"s"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(sum[:])}, "code_challenge_method": {"S256"}}
	if sendRedirect {
		query.Set("redirect_uri", oauthTestCallback)
	}
	back := f.authorize(t, f.issuer+mcpoauth.AuthorizePath+"?"+query.Encode(), "scout", oauthApprove).Query()
	if back.Get("code") == "" {
		t.Fatalf("no code came back: %v", back)
	}
	return approvedCode{clientID: client.ID, code: back.Get("code"), verifier: verifier}
}

func (c approvedCode) exchange(redirect string) url.Values {
	form := url.Values{"grant_type": {"authorization_code"}, "client_id": {c.clientID}, "code": {c.code}, "code_verifier": {c.verifier}}
	if redirect != "" {
		form.Set("redirect_uri", redirect)
	}
	return form
}

func TestACodeIsRedeemedOnlyWithTheRedirectURIItsRequestNamed(t *testing.T) {
	f := newOAuthFixture(t)
	named := f.approvedCode(t, true)
	if status, body := f.token(t, named.exchange("")); status != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("redirect_uri omitted: status=%d body=%v", status, body)
	}
	if status, body := f.token(t, f.approvedCode(t, true).exchange(oauthTestCallback)); status != http.StatusOK {
		t.Fatalf("exact redirect_uri: status=%d body=%v", status, body)
	}
	if status, body := f.token(t, f.approvedCode(t, false).exchange("")); status != http.StatusOK {
		t.Fatalf("a request that named no redirect_uri: status=%d body=%v", status, body)
	}
}

func TestACodeUsedTwiceLeavesNoLiveGrantEvenWhenBothUsesRace(t *testing.T) {
	f := newOAuthFixture(t)
	for round := range 5 {
		code := f.approvedCode(t, true)
		type outcome struct {
			status int
			body   map[string]any
		}
		results := make(chan outcome, 2)
		for range 2 {
			go func() {
				response, err := f.client.PostForm(f.issuer+mcpoauth.TokenPath, code.exchange(oauthTestCallback))
				if err != nil {
					results <- outcome{}
					return
				}
				defer response.Body.Close()
				var body map[string]any
				_ = json.NewDecoder(response.Body).Decode(&body)
				results <- outcome{response.StatusCode, body}
			}()
		}
		both := []outcome{<-results, <-results}
		for _, got := range both {
			if got.status != http.StatusOK {
				continue
			}
			if status, _ := f.probe(t, got.body["access_token"].(string)); status != http.StatusUnauthorized {
				t.Fatalf("round %d: a token issued for a code used twice still works (%d)", round, status)
			}
		}
		if grants, err := f.r.OAuthGrants(); err != nil || len(grants) != 0 {
			t.Fatalf("round %d: grants=%+v err=%v", round, grants, err)
		}
	}
}

func TestApprovalsThatExpireUnansweredFreeTheirPlaceAndWithdrawTheirAsks(t *testing.T) {
	f := newOAuthFixture(t)
	client, err := f.r.RegisterOAuthClient("Kat", []string{oauthTestCallback}, 64, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	submit := func() int {
		form := url.Values{"identity": {"scout"}, "response_type": {"code"}, "client_id": {client.ID}, "redirect_uri": {oauthTestCallback}, "code_challenge": {"E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"}, "code_challenge_method": {"S256"}}
		response, err := f.browser.PostForm(f.issuer+mcpoauth.AuthorizePath, form)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for range oauthOpenAuthorizations {
		if status := submit(); status != http.StatusOK {
			t.Fatalf("an approval within the limit got %d", status)
		}
	}
	if status := submit(); status != http.StatusTooManyRequests {
		t.Fatalf("a ninth open approval got %d", status)
	}
	m := f.d.remoteMCP()
	m.oauth.mu.Lock()
	m.sweepOAuthLocked(time.Now().Add(oauthAuthorizationLife + time.Second))
	m.oauth.mu.Unlock()
	if open, err := f.r.OpenAsks(); err != nil || len(open) != 0 {
		t.Fatalf("expired approvals left asks open: %d err=%v", len(open), err)
	}
	if status := submit(); status != http.StatusOK {
		t.Fatalf("expired approvals still hold their places: %d", status)
	}
}

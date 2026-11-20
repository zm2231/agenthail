package daemon

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/cfaccess"
	"github.com/zm2231/agenthail/internal/cfaccess/cfaccesstest"
	"github.com/zm2231/agenthail/internal/registry"
)

type headerTransport map[string]string

func (h headerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	for key, value := range h {
		r.Header.Set(key, value)
	}
	return http.DefaultTransport.RoundTrip(r)
}

type remoteFixture struct {
	d      *Daemon
	r      *registry.Registry
	server *httptest.Server
	issuer *cfaccesstest.Issuer
	scout  registry.AgentIdentity
	token  string
	log    *bytes.Buffer
}

func newRemoteFixture(t *testing.T, clientID string) *remoteFixture {
	t.Helper()
	d, r, _, _, _ := daemonFixture(t)
	f := &remoteFixture{d: d, r: r, issuer: cfaccesstest.New(t), log: &bytes.Buffer{}}
	d.log = log.New(f.log, "", 0)
	d.remoteMCP().certsURL = f.issuer.CertsURL()
	var err error
	if f.scout, err = r.AddAgentIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	if _, f.token, err = r.BindRemoteIdentity("scout", clientID, ""); err != nil {
		t.Fatal(err)
	}
	f.server = mcpServer(t, d)
	return f
}

func (f *remoteFixture) access(t *testing.T) {
	t.Helper()
	if err := f.r.SetAccessSettings(cfaccesstest.Team, cfaccesstest.AUD); err != nil {
		t.Fatal(err)
	}
}

func (f *remoteFixture) post(t *testing.T, token, assertion string) int {
	t.Helper()
	headers := map[string]string{}
	if token != "" {
		headers["Authorization"] = "Bearer " + token
	}
	if assertion != "" {
		headers[cfaccess.Header] = assertion
	}
	status, _ := postMCP(t, f.server, headers)
	return status
}

func (f *remoteFixture) connect(t *testing.T, headers map[string]string, options *mcp.ClientOptions) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: f.server.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport(headers)}, MaxRetries: -1}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "remote", Version: "1"}, options).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func (f *remoteFixture) deliver(t *testing.T, text string) {
	t.Helper()
	if _, _, err := f.r.StoreInboxMessage(registry.InboxMessage{Identity: f.scout.SessionID, UUID: text, SenderName: "builder", Message: text}); err != nil {
		t.Fatal(err)
	}
}

func TestRemoteMCPBehindAccessNeedsBothCredentialsForTheSameIdentity(t *testing.T) {
	f := newRemoteFixture(t, "client-a.access")
	f.access(t)
	other, err := f.r.AddAgentIdentity("builder")
	if err != nil {
		t.Fatal(err)
	}
	_, otherToken, err := f.r.BindRemoteIdentity(other.Name, "client-b", "")
	if err != nil {
		t.Fatal(err)
	}
	emailIdentity, err := f.r.AddAgentIdentity("helper")
	if err != nil {
		t.Fatal(err)
	}
	_, emailToken, err := f.r.BindRemoteIdentity(emailIdentity.Name, "", "Person@Example.com")
	if err != nil {
		t.Fatal(err)
	}
	strangerKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	with := func(change func(jwt.MapClaims)) string {
		claims := cfaccesstest.Token("client-a")
		change(claims)
		return f.issuer.Sign(t, claims)
	}
	valid := f.issuer.Sign(t, cfaccesstest.Token("client-a"))
	cases := []struct {
		name      string
		token     string
		assertion string
		status    int
	}{
		{"both, client id without suffix", f.token, f.issuer.Sign(t, cfaccesstest.Token("CLIENT-A")), http.StatusOK},
		{"both, client id with suffix", f.token, f.issuer.Sign(t, cfaccesstest.Token("client-a.access")), http.StatusOK},
		{"remote token alone", f.token, "", http.StatusUnauthorized},
		{"Access token alone", "", valid, http.StatusUnauthorized},
		{"another identity's remote token", otherToken, valid, http.StatusUnauthorized},
		{"another identity's Access token", f.token, f.issuer.Sign(t, cfaccesstest.Token("client-b")), http.StatusUnauthorized},
		{"unknown remote token", registry.RemoteTokenPrefix + "unknown", valid, http.StatusUnauthorized},
		{"wrong audience", f.token, with(func(c jwt.MapClaims) { c["aud"] = []string{"other-aud"} }), http.StatusUnauthorized},
		{"wrong issuer", f.token, with(func(c jwt.MapClaims) { c["iss"] = "https://other-team.cloudflareaccess.com" }), http.StatusUnauthorized},
		{"expired", f.token, with(func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-5 * time.Minute).Unix() }), http.StatusUnauthorized},
		{"unknown signing key", f.token, f.issuer.SignWith(t, cfaccesstest.Token("client-a"), strangerKey, "stranger"), http.StatusUnauthorized},
		{"a person's login with the bound client id", f.token, with(func(c jwt.MapClaims) { c["sub"] = "user-1" }), http.StatusUnauthorized},
		{"service token against an email binding", emailToken, with(func(c jwt.MapClaims) { c["email"] = "person@example.com" }), http.StatusUnauthorized},
		{"a person's login against an email binding", emailToken, with(func(c jwt.MapClaims) {
			c["sub"] = "user-1"
			c["email"] = "person@example.com"
			delete(c, "common_name")
		}), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if status := f.post(t, tc.token, tc.assertion); status != tc.status {
				t.Fatalf("status=%d want %d", status, tc.status)
			}
		})
	}
	if keys, err := f.r.AccessKeysStatus(); err != nil || keys.ReadAt.IsZero() || keys.Error != "" {
		t.Fatalf("keys=%+v err=%v", keys, err)
	}
	if strings.Contains(f.log.String(), f.token) {
		t.Fatal("a raw remote token reached the daemon log")
	}
}

func TestRemoteMCPTokenOnlyModeIdentifiesTheIdentityAndEndsWithItsToken(t *testing.T) {
	f := newRemoteFixture(t, "")
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	whoami, refused := callMCP(t, session, "agenthail_whoami", nil)
	if refused || whoami["sessionId"] != f.scout.SessionID {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
	if status := f.post(t, f.token, f.issuer.Sign(t, cfaccesstest.Token("client-a"))); status != http.StatusUnauthorized {
		t.Fatalf("an Access request with no Access settings status=%d", status)
	}
	remote, err := f.r.RemoteIdentityNamed("scout")
	if err != nil || remote.LastSeen.IsZero() {
		t.Fatalf("remote=%+v err=%v", remote, err)
	}
	history, err := f.r.ListHistory(50, "")
	if err != nil {
		t.Fatal(err)
	}
	audited := slices.ContainsFunc(history, func(entry registry.HistoryEntry) bool {
		return entry.Kind == "mcp-remote" && entry.Message == "agenthail_whoami via token only"
	})
	if !audited {
		t.Fatalf("no remote audit entry in %+v", history)
	}

	_, rotated, err := f.r.RotateRemoteIdentity("scout")
	if err != nil {
		t.Fatal(err)
	}
	if status := f.post(t, f.token, ""); status != http.StatusUnauthorized {
		t.Fatalf("the replaced token status=%d", status)
	}
	if status := f.post(t, rotated, ""); status != http.StatusOK {
		t.Fatalf("the new token status=%d", status)
	}
	if err := f.r.UnbindRemoteIdentity("scout"); err != nil {
		t.Fatal(err)
	}
	if status := f.post(t, rotated, ""); status != http.StatusUnauthorized {
		t.Fatalf("after remote access is off status=%d", status)
	}

	for _, path := range []string{f.r.Path(), f.r.Path() + "-wal"} {
		stored, err := os.ReadFile(path)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(stored, []byte(f.token)) || bytes.Contains(stored, []byte(rotated)) {
			t.Fatalf("a raw remote token is stored in %s", path)
		}
	}
}

func TestAccessRequestsReachOnlyMCP(t *testing.T) {
	f := newRemoteFixture(t, "client-a")
	f.access(t)
	assertion := f.issuer.Sign(t, cfaccesstest.Token("client-a"))
	for _, path := range []string{"/api/v1/actions", "/api/v1/sessions", "/", "/mcp/extra"} {
		request, err := http.NewRequestWithContext(t.Context(), http.MethodGet, f.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set(cfaccess.Header, assertion)
		request.Header.Set("Authorization", "Bearer "+dashboardTestToken)
		response, err := http.DefaultClient.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("%s status=%d", path, response.StatusCode)
		}
	}
}

func TestARemoteIdentityGetsTheLocalTools(t *testing.T) {
	f := newRemoteFixture(t, "")
	from, err := f.r.Session("from")
	if err != nil {
		t.Fatal(err)
	}
	names := func(session *mcp.ClientSession) []string {
		tools, err := session.ListTools(t.Context(), nil)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, tool := range tools.Tools {
			out = append(out, tool.Name)
		}
		slices.Sort(out)
		return out
	}
	remote := names(f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil))
	local := names(mcpSession(t, f.server, issueMCPToken(t, f.d, *from)))
	if !slices.Equal(remote, local) {
		t.Fatalf("remote tools %v, local tools %v", remote, local)
	}
}

func TestAnInboxReadReturnsWhatFitsAndLeavesTheRestUnread(t *testing.T) {
	f := newRemoteFixture(t, "")
	for i := range 5 {
		f.deliver(t, strings.Repeat(string(rune('a'+i)), 4000))
	}
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	read, _ := callMCP(t, session, "agenthail_read", nil)
	messages, _ := read["messages"].([]any)
	if len(messages) == 0 || len(messages) == 5 || read["remaining"] != float64(5-len(messages)) {
		t.Fatalf("read %d messages, remaining=%v", len(messages), read["remaining"])
	}
	if unread, err := f.r.UnreadInbox("scout"); err != nil || unread != 5-len(messages) {
		t.Fatalf("unread=%d err=%v", unread, err)
	}
}

func TestAnInboxReadOfEscapeHeavyAndLongMessagesFitsAndLosesNothing(t *testing.T) {
	f := newRemoteFixture(t, "")
	sent := []string{strings.Repeat("\n", 4000), strings.Repeat("\"<q>\"", 3000), strings.Repeat("é", 9000)}
	for _, text := range sent {
		f.deliver(t, text)
	}
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	full := map[float64]string{}
	for round := 0; round < len(sent); round++ {
		result, err := session.CallTool(t.Context(), callParams("agenthail_read", nil))
		if err != nil || result.IsError {
			t.Fatalf("read round %d: err=%v result=%+v", round, err, result)
		}
		encoded, _ := json.Marshal(result.StructuredContent)
		var body map[string]any
		_ = json.Unmarshal(encoded, &body)
		messages, _ := body["messages"].([]any)
		if len(messages) == 0 {
			break
		}
		for _, raw := range messages {
			item := raw.(map[string]any)
			id := item["id"].(float64)
			if item["truncated"] != true {
				full[id] = item["text"].(string)
				continue
			}
			var text strings.Builder
			offset := float64(0)
			for {
				page, refused := callMCP(t, session, "agenthail_read", map[string]any{"bodyRef": item["bodyRef"], "offset": offset})
				if refused {
					t.Fatalf("body page refused: %v", page)
				}
				text.WriteString(page["text"].(string))
				next, more := page["nextOffset"].(float64)
				if !more {
					break
				}
				offset = next
			}
			full[id] = text.String()
		}
	}
	if len(full) != len(sent) {
		t.Fatalf("recovered %d of %d messages", len(full), len(sent))
	}
	for _, text := range sent {
		found := false
		for _, got := range full {
			found = found || got == text
		}
		if !found {
			t.Fatalf("a %d-byte message was not recovered whole", len(text))
		}
	}
}

func TestARemoteIdentityWaitsForItsInboxAndReadsIt(t *testing.T) {
	f := newRemoteFixture(t, "")
	session := f.connect(t, map[string]string{"Authorization": "Bearer " + f.token}, nil)
	waited, refused := callMCP(t, session, "agenthail_wait", map[string]any{"tasks": []string{}, "timeoutSec": 0})
	if refused || waited["type"] != "timeout" {
		t.Fatalf("wait on an empty inbox=%v refused=%v", waited, refused)
	}
	stored := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		var err error
		for _, text := range []string{"first", "second"} {
			if err == nil {
				_, _, err = f.r.StoreInboxMessage(registry.InboxMessage{Identity: f.scout.SessionID, UUID: text, SenderName: "builder", Message: text})
			}
		}
		stored <- err
	}()
	waited, refused = callMCP(t, session, "agenthail_wait", map[string]any{"tasks": []string{}, "timeoutSec": 10})
	if refused || waited["type"] != "inbox" {
		t.Fatalf("wait=%v refused=%v", waited, refused)
	}
	if err := <-stored; err != nil {
		t.Fatal(err)
	}
	read, refused := callMCP(t, session, "agenthail_read", nil)
	messages, _ := read["messages"].([]any)
	if refused || len(messages) != 2 || read["remaining"] != float64(0) {
		t.Fatalf("read=%v refused=%v", read, refused)
	}
	if first, _ := messages[0].(map[string]any); first["text"] != "first" || first["from"] != "builder" {
		t.Fatalf("first=%v", first)
	}
	again, _ := callMCP(t, session, "agenthail_read", nil)
	if rest, _ := again["messages"].([]any); len(rest) != 0 {
		t.Fatalf("a second read returned %v", again)
	}
	if unread, err := f.r.UnreadInbox("scout"); err != nil || unread != 0 {
		t.Fatalf("unread=%d err=%v", unread, err)
	}
}

func TestAnAbandonedInboxReadLeavesItsMessagesUnread(t *testing.T) {
	f := newRemoteFixture(t, "")
	f.deliver(t, "keep me")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := f.d.mcpReadInbox(ctx, f.scout); err == nil {
		t.Fatal("a cancelled inbox read succeeded")
	}
	if unread, err := f.r.UnreadInbox("scout"); err != nil || unread != 1 {
		t.Fatalf("unread=%d err=%v after a cancelled read", unread, err)
	}
}

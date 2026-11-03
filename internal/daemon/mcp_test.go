package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/surface"
)

const mcpInitialize = `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1"}}}`

type bearerTransport struct{ token string }

func (b bearerTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+b.token)
	return http.DefaultTransport.RoundTrip(r)
}

func mcpServer(t *testing.T, d *Daemon) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	return server
}

func issueMCPToken(t *testing.T, d *Daemon, session surface.Session) string {
	t.Helper()
	issued, err := d.issueSessionCredential(session)
	if err != nil || issued.Token == "" {
		t.Fatalf("issued=%+v err=%v", issued, err)
	}
	return issued.Token
}

func mcpSession(t *testing.T, server *httptest.Server, token string) *mcp.ClientSession {
	t.Helper()
	transport := &mcp.StreamableClientTransport{Endpoint: server.URL + "/mcp", HTTPClient: &http.Client{Transport: bearerTransport{token: token}}, MaxRetries: -1, DisableStandaloneSSE: true}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	return session
}

func callMCP(t *testing.T, session *mcp.ClientSession, tool string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	result, err := session.CallTool(t.Context(), callParams(tool, args))
	if err != nil {
		t.Fatalf("%s: %v", tool, err)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(encoded, &out); err != nil {
		t.Fatalf("%s structured content %s: %v", tool, encoded, err)
	}
	return out, result.IsError
}

func callParams(tool string, args map[string]any) *mcp.CallToolParams {
	if args == nil {
		args = map[string]any{}
	}
	return &mcp.CallToolParams{Name: tool, Arguments: args}
}

func postMCP(t *testing.T, server *httptest.Server, headers map[string]string) (int, map[string]any) {
	t.Helper()
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", strings.NewReader(mcpInitialize))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var body map[string]any
	_ = json.NewDecoder(response.Body).Decode(&body)
	return response.StatusCode, body
}

func errorCode(body map[string]any) string {
	failure, _ := body["error"].(map[string]any)
	code, _ := failure["code"].(string)
	return code
}

func TestMCPRefusesRequestsBeforeAnyMCPMethodRuns(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	cases := []struct {
		name    string
		headers map[string]string
		status  int
		code    string
	}{
		{"no credential", nil, http.StatusUnauthorized, "unauthenticated"},
		{"dashboard token", map[string]string{"Authorization": "Bearer " + dashboardTestToken}, http.StatusUnauthorized, "unauthenticated"},
		{"unknown session token", map[string]string{"Authorization": "Bearer ahs_unknown"}, http.StatusUnauthorized, "unauthenticated"},
		{"proxied from the tailnet", map[string]string{"Authorization": "Bearer " + token, "X-Forwarded-For": "100.64.0.9"}, http.StatusForbidden, "remote_refused"},
		{"tailscale identity", map[string]string{"Authorization": "Bearer " + token, "Tailscale-User-Login": "someone@example.com"}, http.StatusForbidden, "remote_refused"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, body := postMCP(t, server, tc.headers)
			if status != tc.status || errorCode(body) != tc.code {
				t.Fatalf("status=%d body=%v", status, body)
			}
		})
	}
	status, _ := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token, "Origin": "https://attacker.example", "Sec-Fetch-Site": "cross-site"})
	if status != http.StatusForbidden {
		t.Fatalf("foreign origin status=%d", status)
	}
	if status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status != http.StatusOK {
		t.Fatalf("valid credential status=%d body=%v", status, body)
	}
}

func TestMCPWhoamiNamesTheCallingSession(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	if err := r.SetAlias("worker", from.ID); err != nil {
		t.Fatal(err)
	}
	var logged bytes.Buffer
	d.log = log.New(&logged, "", 0)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	whoami, refused := callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil)
	if refused || whoami["sessionId"] != from.ID || whoami["alias"] != "worker" || whoami["coordinator"] != false {
		t.Fatalf("whoami=%v refused=%v", whoami, refused)
	}
	capabilities, _ := whoami["capabilities"].([]any)
	if len(capabilities) != len(defaultSessionCapabilities) {
		t.Fatalf("capabilities=%v", capabilities)
	}
	if strings.Contains(logged.String(), token) {
		t.Fatal("the raw token reached the daemon log")
	}
}

func TestMCPExpiredCredentialWorksAgainAfterTheNextTurn(t *testing.T) {
	d, r, fake, from, _ := daemonFixture(t)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	if _, err := r.WriteDB().Exec(`UPDATE session_credentials SET idle_expires_at_ms=? WHERE session_id=?`, time.Now().Add(-time.Minute).UnixMilli(), from.ID); err != nil {
		t.Fatal(err)
	}
	header := map[string]string{"Authorization": "Bearer " + token}
	if status, body := postMCP(t, server, header); status != http.StatusUnauthorized || errorCode(body) != "credential_expired" || body["retryable"] != true {
		t.Fatalf("expired status=%d body=%v", status, body)
	}
	fake.observations[from.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}
	d.observeSession(context.Background(), fake, &from)
	if whoami, refused := callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil); refused || whoami["sessionId"] != from.ID {
		t.Fatalf("revived whoami=%v refused=%v", whoami, refused)
	}
}

func TestMCPCredentialEndsWithItsSession(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	if err := r.ArchiveSession(from.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token})
	if status != http.StatusUnauthorized || errorCode(body) != "credential_revoked" {
		t.Fatalf("status=%d body=%v", status, body)
	}
}

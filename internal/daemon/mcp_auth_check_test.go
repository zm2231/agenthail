package daemon

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestASessionCredentialThatCannotBeCheckedIsABusyRefusalNotA401(t *testing.T) {
	d, r, _, from, _ := daemonFixture(t)
	server := mcpServer(t, d)
	token := issueMCPToken(t, d, from)
	client := mcpSession(t, server, token)
	if err := r.ReadDB().Close(); err != nil {
		t.Fatal(err)
	}
	if status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status == http.StatusUnauthorized {
		t.Fatalf("a credential the daemon could not read answered 401: %v", body)
	}
	out, refused := callMCP(t, client, "agenthail_whoami", nil)
	if !refused || out["code"] != "busy" || out["retryable"] != true {
		t.Fatalf("a call whose credential could not be checked = %v (refused %v), want a retryable busy refusal", out, refused)
	}
}

func TestACancelledCredentialCheckIsNeverA401(t *testing.T) {
	d, _, _, from, _ := daemonFixture(t)
	token := issueMCPToken(t, d, from)
	handler := d.mcpHandler()
	for range 200 {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		request := httptest.NewRequestWithContext(ctx, http.MethodPost, "/mcp", strings.NewReader(mcpInitialize))
		request.RemoteAddr = "127.0.0.1:1"
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("Accept", "application/json, text/event-stream")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code == http.StatusUnauthorized {
			t.Fatalf("a cancelled check of a live credential answered 401: %s", response.Body)
		}
	}
}

func TestASocketCallerTheDaemonCannotReadIsABusyRefusal(t *testing.T) {
	d, reg, root, _ := mcpSocketDaemon(t)
	registerClaude(t, reg, "claude-one")
	parent := &relayedSession{record: filepath.Join(root, "sessions", strconv.Itoa(os.Getppid())+".json")}
	parent.writeFor(t, os.Getppid(), "claude-one", true)
	client := socketClient(t)
	if err := d.Registry.ReadDB().Close(); err != nil {
		t.Fatal(err)
	}
	out, refused := callMCP(t, client, "agenthail_whoami", nil)
	if !refused || out["code"] != "busy" || out["retryable"] != true {
		t.Fatalf("a socket call whose session could not be read = %v (refused %v), want a retryable busy refusal", out, refused)
	}
}

func TestAnIdleCredentialWhoseSessionCannotBeReadIsABusyRefusalNotA401(t *testing.T) {
	d, r, _, from, token, _ := credentialFixture(t)
	server := mcpServer(t, d)
	client := mcpSession(t, server, token)
	idleFor(t, r, from.ID, "idle_expires_at_ms")
	if _, err := r.WriteDB().Exec(`ALTER TABLE catalog_sessions RENAME TO catalog_sessions_gone`); err != nil {
		t.Fatal(err)
	}
	if status, body := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status == http.StatusUnauthorized {
		t.Fatalf("an idle credential whose session could not be read answered 401: %v", body)
	}
	out, refused := callMCP(t, client, "agenthail_whoami", nil)
	if !refused || out["code"] != "busy" || out["retryable"] != true {
		t.Fatalf("a call whose idle credential could not be checked = %v (refused %v), want a retryable busy refusal", out, refused)
	}
}

func TestEveryMCPAuthRefusalIsLoggedWithoutTheToken(t *testing.T) {
	d, r, _, from, token, logged := credentialFixture(t)
	if err := r.SetAlias("quiet", from.ID); err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	if err := r.RevokeSessionCredentials(from.ID, "revoked", time.Now()); err != nil {
		t.Fatal(err)
	}
	if status, _ := postMCP(t, server, map[string]string{"Authorization": "Bearer " + token}); status != http.StatusUnauthorized {
		t.Fatalf("a revoked credential answered %d", status)
	}
	if status, _ := postMCP(t, server, map[string]string{"Authorization": "Bearer ahs_never_issued"}); status != http.StatusUnauthorized {
		t.Fatalf("an unknown credential answered %d", status)
	}
	lines := logged.String()
	for _, want := range []string{"mcp auth session=@quiet path=http outcome=refused:credential_revoked:revoked", "mcp auth session=- path=http outcome=refused:unauthenticated"} {
		if !strings.Contains(lines, want) {
			t.Errorf("the daemon log has no %q:\n%s", want, lines)
		}
	}
	if strings.Contains(lines, token) || strings.Contains(lines, "ahs_never_issued") {
		t.Fatalf("the daemon log holds a token:\n%s", lines)
	}
}

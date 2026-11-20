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

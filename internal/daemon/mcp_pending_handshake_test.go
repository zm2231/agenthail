package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAPendingTokenListsToolsButEveryCallIsRefusedUntilItBinds(t *testing.T) {
	d, r, _, _, child := daemonFixture(t)
	pending, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, d), pending.Token)
	tools, err := session.ListTools(t.Context(), &mcp.ListToolsParams{})
	if err != nil || len(tools.Tools) == 0 {
		t.Fatalf("tools=%v err=%v", tools, err)
	}
	for _, tool := range tools.Tools {
		result, err := session.CallTool(t.Context(), callParams(tool.Name, map[string]any{}))
		if err != nil || !result.IsError {
			t.Fatalf("%s with a pending token: result=%+v err=%v", tool.Name, result, err)
		}
	}
	if out, refused := callMCP(t, session, "agenthail_whoami", map[string]any{}); !refused || out["code"] != "unauthenticated" || out["retryable"] != true {
		t.Fatalf("refused=%v out=%v", refused, out)
	}
	if claimed, err := r.ClaimPendingCredential(pending.Ref, child.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if out, refused := callMCP(t, session, "agenthail_whoami", map[string]any{}); refused || out["sessionId"] != child.ID {
		t.Fatalf("after binding: refused=%v out=%v", refused, out)
	}
}

func TestOnlyALivePendingTokenPassesTheHandshake(t *testing.T) {
	d, r, _, _, child := daemonFixture(t)
	server := mcpServer(t, d)
	bearer := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }

	live, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := postMCP(t, server, bearer(live.Token)); status != http.StatusOK {
		t.Fatalf("live pending token: status=%d body=%v", status, body)
	}

	expired, err := r.IssuePendingCredential(nil, time.Now().Add(-2*time.Minute), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if status, body := postMCP(t, server, bearer(expired.Token)); status != http.StatusUnauthorized || errorCode(body) != "unauthenticated" {
		t.Fatalf("expired pending token: status=%d body=%v", status, body)
	}

	if status, body := postMCP(t, server, bearer("ahs_never_issued")); status != http.StatusUnauthorized || errorCode(body) != "unauthenticated" {
		t.Fatalf("unknown token: status=%d body=%v", status, body)
	}

	used, err := r.IssuePendingCredential(nil, time.Now(), time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if claimed, err := r.ClaimPendingCredential(used.Ref, child.ID, time.Now()); err != nil || !claimed {
		t.Fatalf("claimed=%v err=%v", claimed, err)
	}
	if _, err := r.RotateSessionCredential(child.ID, time.Now()); err != nil {
		t.Fatal(err)
	}
	if status, body := postMCP(t, server, bearer(used.Token)); status != http.StatusUnauthorized || errorCode(body) != "credential_revoked" {
		t.Fatalf("used token after rotation: status=%d body=%v", status, body)
	}
}

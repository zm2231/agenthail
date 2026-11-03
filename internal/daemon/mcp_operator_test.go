package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func callerCapabilities(t *testing.T, server *httptest.Server, token string) []any {
	t.Helper()
	whoami, refused := callMCP(t, mcpSession(t, server, token), "agenthail_whoami", nil)
	if refused {
		t.Fatalf("whoami refused: %v", whoami)
	}
	capabilities, _ := whoami["capabilities"].([]any)
	return capabilities
}

func asOperator(t *testing.T, d *Daemon, server *httptest.Server, sessionID string) *mcp.ClientSession {
	t.Helper()
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	session, err := d.Registry.Session(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	token, err := d.mcpCredentials.Load().Rotate(sessionID)
	if err != nil {
		t.Fatal(err)
	}
	makeVoiceOperator(t, d, *session)
	if caps := callerCapabilities(t, server, token); !slices.Contains(caps, any("control.all")) {
		t.Fatalf("%s did not become the operator: capabilities=%v", sessionID, caps)
	}
	return mcpSession(t, server, token)
}

func makeVoiceOperator(t *testing.T, d *Daemon, session surface.Session) {
	t.Helper()
	if err := d.Registry.RecordSessionOrigin(registry.SessionOrigin{SessionID: session.ID, Via: registry.OriginVoice}); err != nil {
		t.Fatal(err)
	}
	if err := d.claimVoiceAlias(session); err != nil {
		t.Fatal(err)
	}
}

func TestOnlyTheCurrentVoiceOperatorHoldsControlOfEverySession(t *testing.T) {
	d, r, fake, first, second := daemonFixture(t)
	fake.caps = surface.Capabilities{Send: true, Steer: true, Interrupt: true}
	server := mcpServer(t, d)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	firstToken := issueMCPToken(t, d, first)
	secondToken := issueMCPToken(t, d, second)
	makeVoiceOperator(t, d, first)
	if caps := callerCapabilities(t, server, firstToken); !slices.Contains(caps, any("control.all")) || !slices.Contains(caps, any("read.all")) {
		t.Fatalf("operator caps=%v", caps)
	}
	if out, refused := callMCP(t, mcpSession(t, server, firstToken), "agenthail_steer", map[string]any{"target": second.ID, "message": "operator steer", "clientRequestId": "o1"}); refused {
		t.Fatalf("operator steering a session it did not spawn: %v", out)
	}
	makeVoiceOperator(t, d, second)
	if caps := callerCapabilities(t, server, firstToken); slices.Contains(caps, any("control.all")) || slices.Contains(caps, any("read.all")) {
		t.Fatalf("replaced operator kept caps=%v", caps)
	}
	if caps := callerCapabilities(t, server, secondToken); !slices.Contains(caps, any("control.all")) {
		t.Fatalf("new operator caps=%v", caps)
	}
	for _, session := range []surface.Session{first, second} {
		if rows := credentialRows(t, r, session.ID); rows != 1 {
			t.Fatalf("%s credentials=%d, an operator change minted a token", session.ID, rows)
		}
	}
}

func TestAReplacedOperatorsRetryReplaysItsFirstResult(t *testing.T) {
	d, r, _, first, second := daemonFixture(t)
	server := mcpServer(t, d)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	token := issueMCPToken(t, d, first)
	makeVoiceOperator(t, d, first)
	args := map[string]any{"action": "create", "channel": "ops", "clientRequestId": "c1"}
	created, refused := callMCP(t, mcpSession(t, server, token), "agenthail_channel", args)
	if refused {
		t.Fatalf("create: %v", created)
	}
	makeVoiceOperator(t, d, second)
	if replayed, refused := callMCP(t, mcpSession(t, server, token), "agenthail_channel", args); refused || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("retry after replacement=%v refused=%v, want the first result %v", replayed, refused, created)
	}
	if out, _ := callMCP(t, mcpSession(t, server, token), "agenthail_channel", map[string]any{"action": "create", "channel": "other", "clientRequestId": "c1"}); out["code"] != "idempotency_conflict" {
		t.Fatalf("changed arguments under the same id: %v", out)
	}
	if out, _ := callMCP(t, mcpSession(t, server, token), "agenthail_channel", map[string]any{"action": "create", "channel": "other", "clientRequestId": "c2"}); out["code"] != "capability_missing" {
		t.Fatalf("a new request from the replaced operator: %v", out)
	}
	channels, err := r.ListChannels()
	if err != nil || len(channels) != 1 {
		t.Fatalf("channels=%v err=%v, the retry acted twice", channels, err)
	}
}

func postMCPToolCall(t *testing.T, server *httptest.Server, token, tool string, args map[string]any) (int, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": 7, "method": "tools/call", "params": map[string]any{"name": tool, "arguments": args}})
	if err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/mcp", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Authorization", "Bearer "+token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var out map[string]any
	_ = json.NewDecoder(response.Body).Decode(&out)
	return response.StatusCode, out
}

func TestAnEndedSessionsRetryIsRefusedNotReplayed(t *testing.T) {
	for name, end := range map[string]func(*registry.Registry, string) error{
		"archived": func(r *registry.Registry, id string) error { return r.ArchiveSession(id, time.Now()) },
		"revoked": func(r *registry.Registry, id string) error {
			return r.RevokeSessionCredentials(id, registry.RevokedByPerson, time.Now())
		},
	} {
		t.Run(name, func(t *testing.T) {
			d, r, _, first, _ := daemonFixture(t)
			server := mcpServer(t, d)
			d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
			token := issueMCPToken(t, d, first)
			makeVoiceOperator(t, d, first)
			args := map[string]any{"action": "create", "channel": "ops", "clientRequestId": "c1"}
			if out, refused := callMCP(t, mcpSession(t, server, token), "agenthail_channel", args); refused {
				t.Fatalf("create: %v", out)
			}
			if err := end(r, first.ID); err != nil {
				t.Fatal(err)
			}
			status, body := postMCPToolCall(t, server, token, "agenthail_channel", args)
			if status != http.StatusUnauthorized || errorCode(body) != "credential_revoked" {
				t.Fatalf("retry status=%d body=%v, want it refused before any replay", status, body)
			}
		})
	}
}

func TestARetryWhoseRequestIDDiffersOnlyInSpacesReplays(t *testing.T) {
	d, r, _, first, _ := daemonFixture(t)
	server := mcpServer(t, d)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	token := issueMCPToken(t, d, first)
	makeVoiceOperator(t, d, first)
	created, refused := callMCP(t, mcpSession(t, server, token), "agenthail_channel", map[string]any{"action": "create", "channel": "ops", "clientRequestId": " c1 "})
	if refused {
		t.Fatalf("create: %v", created)
	}
	if replayed, refused := callMCP(t, mcpSession(t, server, token), "agenthail_channel", map[string]any{"action": "create", "channel": "ops", "clientRequestId": "c1"}); refused || !reflect.DeepEqual(replayed, created) {
		t.Fatalf("retry=%v refused=%v, want %v", replayed, refused, created)
	}
	if channels, err := r.ListChannels(); err != nil || len(channels) != 1 {
		t.Fatalf("channels=%v err=%v", channels, err)
	}
}

func TestASessionNamedVoiceThatVoiceDidNotStartGetsNoOperatorPowers(t *testing.T) {
	d, r, _, named, _ := daemonFixture(t)
	server := mcpServer(t, d)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	token := issueMCPToken(t, d, named)
	if err := r.SetAlias(registry.VoiceOperatorAlias, named.ID); err != nil {
		t.Fatal(err)
	}
	if caps := callerCapabilities(t, server, token); slices.Contains(caps, any("control.all")) {
		t.Fatalf("caps=%v", caps)
	}
}

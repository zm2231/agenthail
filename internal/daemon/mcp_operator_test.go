package daemon

import (
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

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

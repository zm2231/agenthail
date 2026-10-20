package daemon

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

func TestSessionAdoptActionOpensAClaudeSessionInTheHostAndReRegistersIt(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, registry, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("HOME", fixture.Home)
	const conversation = "3f1d2c4b-5a6e-4f70-8a91-b2c3d4e5f607"
	transcript := fixture.Transcript(conversation, fixture.Home)
	if err := registry.RegisterSession(surface.Session{ID: conversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: fixture.Home, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	d := New(registry, []surface.Surface{surfaces.NewClaude(nil, fixture.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	adopt := func(stopTerminal bool) *httptest.ResponseRecorder {
		body, _ := json.Marshal(map[string]any{"action": "session-adopt", "sessionId": conversation, "stopTerminal": stopTerminal})
		request := httptest.NewRequest(http.MethodPost, "/api/v1/actions", bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}

	fixture.RecordStatus(conversation, fixture.Home, "", "busy", "", time.Time{})
	if response := adopt(true); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"busy":"a turn is running"`) {
		t.Fatalf("busy adoption: %d %s", response.Code, response.Body.String())
	}
	fixture.Record(conversation, fixture.Home, "")
	if response := adopt(false); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"needsConsent":true`) {
		t.Fatalf("terminal adoption without consent: %d %s", response.Code, response.Body.String())
	}
	response := adopt(true)
	var reply struct {
		OK     bool                `json:"ok"`
		Result surface.AdoptResult `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &reply); err != nil || response.Code != http.StatusOK || !reply.OK || reply.Result.Session == nil {
		t.Fatalf("adopt: %d %s", response.Code, response.Body.String())
	}
	stored, err := registry.Session(conversation)
	if err != nil || stored.RuntimeKind != surface.RuntimeClaudeHost {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
}

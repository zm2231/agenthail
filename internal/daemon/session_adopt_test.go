package daemon

import (
	"bytes"
	"context"
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

func TestTheSnapshotOffersAdoptionForTerminalAndBackgroundSessionsUntilTheHostRunsThem(t *testing.T) {
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	_, registry, _, _, _ := daemonFixture(t)
	fixture := claudetest.New(t)
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	t.Setenv("HOME", fixture.Home)
	const conversation = "6a2e9c1d-4b3f-4e8a-9d07-c1b2a3f4e5d6"
	fixture.Transcript(conversation, fixture.Home)
	d := New(registry, []surface.Surface{surfaces.NewClaude(nil, fixture.Home)})
	handler := d.dashboardHandler(&dashboardServer{token: "secret"})
	serve := func(method, path string, body []byte) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, bytes.NewReader(body))
		request.Header.Set("Authorization", "Bearer secret")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	lifecycle := func() (surface.RuntimeKind, surface.Lifecycle) {
		t.Helper()
		d.discoverCatalog(context.Background())
		response := serve(http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
		var snapshot struct {
			Sessions []struct {
				ID           string              `json:"id"`
				RuntimeKind  surface.RuntimeKind `json:"runtimeKind"`
				Capabilities struct {
					Lifecycle surface.Lifecycle `json:"lifecycle"`
				} `json:"capabilities"`
			} `json:"sessions"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil || response.Code != http.StatusOK {
			t.Fatalf("snapshot: %d %s", response.Code, response.Body.String())
		}
		for _, session := range snapshot.Sessions {
			if session.ID == conversation {
				return session.RuntimeKind, session.Capabilities.Lifecycle
			}
		}
		t.Fatalf("snapshot has no session %s: %s", conversation, response.Body.String())
		return "", surface.Lifecycle{}
	}

	fixture.Listen()
	fixture.BackgroundRecord(conversation, fixture.Home)
	if runtime, got := lifecycle(); runtime != surface.RuntimeClaudeBackground || !got.Adopt || !got.Stop {
		t.Fatalf("background job runtime=%s lifecycle=%+v, want adopt with its job operations", runtime, got)
	}
	fixture.Record(conversation, fixture.Home, "")
	if runtime, got := lifecycle(); runtime != surface.RuntimeClaudeCLI || got != (surface.Lifecycle{Adopt: true}) {
		t.Fatalf("terminal session runtime=%s lifecycle=%+v, want adopt alone", runtime, got)
	}
	body, _ := json.Marshal(map[string]any{"action": "session-adopt", "sessionId": conversation, "stopTerminal": true})
	if response := serve(http.MethodPost, "/api/v1/actions", body); response.Code != http.StatusOK {
		t.Fatalf("adopt: %d %s", response.Code, response.Body.String())
	}
	if runtime, got := lifecycle(); runtime != surface.RuntimeClaudeHost || got.Adopt || !got.Stop {
		t.Fatalf("host session runtime=%s lifecycle=%+v, want its operations without adopt", runtime, got)
	}
}

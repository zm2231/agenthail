package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestTheRequestsAPIDescribesARequestAsAgentsReadIt(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	base := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}, accepted: true}
	responder := &requestingSurface{daemonSurface: base}
	d := newTestDaemon(t, r, []surface.Surface{responder})
	long := strings.Repeat("é", mcpItemTextLimit)
	responder.open = []surface.Request{
		{ID: "run", Kind: surface.WaitingApproval, Tool: "Bash", Input: json.RawMessage(`{"command":"make release"}`), CreatedAt: time.Now()},
		{ID: "long", Kind: surface.WaitingApproval, Tool: "Bash", Input: json.RawMessage(`{"command":"` + long + `"}`), CreatedAt: time.Now()},
	}
	worker := surface.Session{ID: "w", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost, Status: surface.StatusIdle}
	if err := r.RecordObservedSession(worker); err != nil {
		t.Fatal(err)
	}
	base.sessions["w"] = worker
	d.syncWaits(worker, &surface.Waiting{Kind: surface.WaitingApproval, ID: "run", Answerable: true, Open: []string{"run", "long"}})
	response := httptest.NewRecorder()
	d.requestsHandler(response, httptest.NewRequest(http.MethodGet, "/api/v1/requests?sessionId=w", nil))
	var body struct {
		Requests []struct {
			ID     string `json:"id"`
			Detail string `json:"detail"`
		} `json:"requests"`
	}
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &body) != nil || len(body.Requests) != 2 {
		t.Fatalf("requests answered %d %s", response.Code, response.Body)
	}
	if body.Requests[0].Detail != "make release" {
		t.Fatalf("a command request's detail is %q, want the command", body.Requests[0].Detail)
	}
	if detail := body.Requests[1].Detail; !utf8.ValidString(detail) || len(detail) > mcpItemTextLimit || !strings.HasPrefix(long, detail) || detail == "" {
		t.Fatalf("a long command's detail must be cut on a character within %d bytes, got %d bytes", mcpItemTextLimit, len(detail))
	}
}

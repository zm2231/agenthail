package daemon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// askingModel asks the person through agenthail_ask on a turn's first
// request and ends the turn once the tool has answered.
type askingModel struct {
	mu       sync.Mutex
	requests []string
}

func (m *askingModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	var request struct {
		Input []struct {
			Type string `json:"type"`
		} `json:"input"`
	}
	_ = json.Unmarshal(body, &request)
	answered := len(request.Input) > 0 && request.Input[len(request.Input)-1].Type == "function_call_output"
	m.mu.Lock()
	m.requests = append(m.requests, string(body))
	id := fmt.Sprintf("resp_%d", len(m.requests))
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	if answered {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "Asked."}}}})
	} else {
		arguments, _ := json.Marshal(map[string]any{"title": "Which database?", "questions": []any{map[string]any{"question": "Which database should the notes app use?", "options": []any{map[string]any{"label": "SQLite"}, map[string]any{"label": "Postgres"}}}}})
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "function_call", "id": "fc_" + id, "call_id": "call_" + id, "namespace": "mcp__agenthail", "name": "agenthail_ask", "arguments": string(arguments)}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func (m *askingModel) request(index int) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	if index >= len(m.requests) {
		return ""
	}
	return m.requests[index]
}

func (m *askingModel) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.requests)
}

// deferringModel is a model Codex's catalog lets search tools, so a Codex
// that lists deferred namespaces lists the MCP tools that way, as it did in
// the person's session.
const deferringModel = "gpt-5.6-terra"

func TestACodexThreadAgenthailStartsKnowsItRunsInAgenthailAndAsksThePersonThroughIt(t *testing.T) {
	model := &askingModel{}
	real := codextest.StartReal(t, model)
	codex := codexsurface.NewCodexIn(codexsurface.CodexHome{Codex: real.CodexHome, State: filepath.Join(real.Home, ".agenthail")}, "")
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	d := newTestDaemon(t, reg, []surface.Surface{codex})
	server := httptest.NewServer(d.dashboardHandler(&dashboardServer{token: dashboardTestToken}))
	t.Cleanup(server.Close)
	d.attachMCPCredentials(strings.TrimPrefix(server.URL, "http://"))
	cwd := filepath.Join(real.Home, "work", "notes")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	router := dashboardRouter(d)
	action := func(body map[string]any) map[string]any {
		data, _ := json.Marshal(body)
		response := serveDashboardRequest(router, http.MethodPost, "/api/action", string(data))
		var decoded map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		if response.Code >= 300 {
			t.Fatalf("%v: status %d: %s", body["action"], response.Code, response.Body.String())
		}
		return decoded
	}
	created := action(map[string]any{"action": "session-create", "surface": "codex", "message": "Ask me which database to use.", "cwd": cwd, "alias": "notes", "model": deferringModel})
	session, _ := created["session"].(map[string]any)
	id, _ := session["id"].(string)
	if id == "" {
		t.Fatalf("session-create answered %v", created)
	}

	testkit.Eventually(t, "the ask the thread raised through agenthail_ask", 60*time.Second, func() bool {
		open, err := reg.OpenAsks()
		return err == nil && len(open) == 1 && open[0].SessionID == id
	})
	first := model.request(0)
	listed := strings.Contains(first, "mcp__agenthail")
	described := strings.Contains(first, `mcp__agenthail: Agenthail connects you to the person who runs you`) ||
		strings.Contains(first, `"name":"mcp__agenthail","description":"Agenthail connects you to the person who runs you`)
	if listed && !described {
		t.Errorf("the model's first request lists mcp__agenthail without the description its server sends")
	}
	for what, want := range map[string]string{
		"the Agenthail guide in its developer instructions": `You run inside Agenthail, which connects you to the person`,
		"its card, naming it":                               `[Agenthail] You are @notes`,
	} {
		if !strings.Contains(first, want) {
			t.Errorf("the model's first request lacks %s (%q)", what, want)
		}
	}
	if t.Failed() {
		t.FailNow()
	}

	app := dialAppServer(t, real.Socket())
	testkit.Eventually(t, "the first turn to end", 60*time.Second, func() bool {
		read := app.call(t, "thread/read", map[string]any{"threadId": id, "includeTurns": true})
		thread, _ := read["thread"].(map[string]any)
		turns, _ := thread["turns"].([]any)
		last, _ := turns[len(turns)-1].(map[string]any)
		return len(turns) == 1 && last["status"] == "completed"
	})
	before := model.count()
	action(map[string]any{"action": "send", "sessionId": id, "message": "Ask again."})
	testkit.Eventually(t, "the second turn's model request", 60*time.Second, func() bool { return model.count() > before })
	if again := model.request(before); strings.Count(again, `[Agenthail] You are @notes`) != 1 {
		t.Fatalf("the second turn's request carries the card %d times, want once from the first turn", strings.Count(again, `[Agenthail] You are @notes`))
	}
}

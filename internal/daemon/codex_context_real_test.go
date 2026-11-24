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

	"github.com/zm2231/agenthail/internal/agentcard"
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

func developerTexts(request string) []string {
	var decoded struct {
		Input []struct {
			Role    string `json:"role"`
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	_ = json.Unmarshal([]byte(request), &decoded)
	var texts []string
	for _, item := range decoded.Input {
		if item.Role != "developer" {
			continue
		}
		for _, content := range item.Content {
			texts = append(texts, content.Text)
		}
	}
	return texts
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

type codexContextAction func(body map[string]any) map[string]any

// codexContextDaemon runs a daemon with its agenthail MCP server over a real
// Codex app-server whose model is model.
func codexContextDaemon(t *testing.T, model http.Handler) (*registry.Registry, *codextest.Real, string, codexContextAction) {
	t.Helper()
	real := codextest.StartRealWith(t, model, "[model_providers.mock.capabilities]\nremote_compaction = \"v2\"\n")
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
	return reg, real, cwd, func(body map[string]any) map[string]any {
		data, _ := json.Marshal(body)
		response := serveDashboardRequest(router, http.MethodPost, "/api/action", string(data))
		var decoded map[string]any
		_ = json.Unmarshal(response.Body.Bytes(), &decoded)
		if response.Code >= 300 {
			t.Fatalf("%v: status %d: %s", body["action"], response.Code, response.Body.String())
		}
		return decoded
	}
}

func awaitCodexTurns(t *testing.T, app *appServer, threadID string, count int) {
	t.Helper()
	testkit.Eventually(t, fmt.Sprintf("turn %d to end", count), 60*time.Second, func() bool {
		read := app.call(t, "thread/read", map[string]any{"threadId": threadID, "includeTurns": true})
		thread, _ := read["thread"].(map[string]any)
		turns, _ := thread["turns"].([]any)
		if len(turns) != count {
			return false
		}
		last, _ := turns[len(turns)-1].(map[string]any)
		return last["status"] == "completed"
	})
}

// replyingModel answers every turn with a message and every remote
// compaction with a compaction item.
type replyingModel struct{ askingModel }

func (m *replyingModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
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
	if strings.Contains(string(body), "compaction_trigger") {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "compaction", "encrypted_content": "summary"}})
	} else {
		send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "Done."}}}})
	}
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 10, "output_tokens": 1, "total_tokens": 11, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func TestACodexThreadKeepsItsCardOnceAcrossEachCompaction(t *testing.T) {
	model := &replyingModel{}
	_, real, cwd, action := codexContextDaemon(t, model)
	created := action(map[string]any{"action": "session-create", "surface": "codex", "message": "Start the notes app.", "cwd": cwd, "alias": "notes", "model": deferringModel})
	id, _ := created["session"].(map[string]any)["id"].(string)
	app := dialAppServer(t, real.Socket())
	turns := 1
	awaitCodexTurns(t, app, id, turns)
	for round := 1; round <= 2; round++ {
		app.call(t, "thread/compact/start", map[string]any{"threadId": id})
		turns++
		awaitCodexTurns(t, app, id, turns)
		message := fmt.Sprintf("Carry on after compaction %d.", round)
		action(map[string]any{"action": "send", "sessionId": id, "message": message})
		turns++
		var after string
		testkit.Eventually(t, "the model request after compaction", 60*time.Second, func() bool {
			for index := model.count() - 1; index >= 0; index-- {
				if request := model.request(index); strings.Contains(request, message) {
					after = request
					return true
				}
			}
			return false
		})
		if told := strings.Count(after, "You are @notes"); told != 1 {
			t.Fatalf("after compaction %d the thread is told who it is %d times, want once", round, told)
		}
		awaitCodexTurns(t, app, id, turns)
	}
}

func TestACodexThreadAgenthailStartsKnowsItRunsInAgenthailAndAsksThePersonThroughIt(t *testing.T) {
	model := &askingModel{}
	reg, real, cwd, action := codexContextDaemon(t, model)
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
	described := strings.Contains(first, `mcp__agenthail: Agenthail connects you to the person and to other agent sessions`) ||
		strings.Contains(first, `"name":"mcp__agenthail","description":"Agenthail connects you to the person and to other agent sessions`)
	if listed && !described {
		t.Errorf("the model's first request lists mcp__agenthail without the description its server sends")
	}
	guided, carded := false, false
	for _, text := range developerTexts(first) {
		card := strings.Contains(text, "[Agenthail] You are @notes")
		guided = guided || !card && strings.Contains(text, agentcard.CodexGuide())
		carded = carded || card
	}
	if !guided {
		t.Errorf("the model's first request has no developer instructions carrying the Agenthail guide")
	}
	if !carded {
		t.Errorf("the model's first request has no developer message with the thread's card")
	}
	if t.Failed() {
		t.FailNow()
	}

	awaitCodexTurns(t, dialAppServer(t, real.Socket()), id, 1)
	before := model.count()
	action(map[string]any{"action": "send", "sessionId": id, "message": "Ask again."})
	testkit.Eventually(t, "the second turn's model request", 60*time.Second, func() bool { return model.count() > before })
	if told := strings.Count(model.request(before), "You are @notes"); told != 1 {
		t.Fatalf("the second turn's request tells the thread who it is %d times, want once, from the first turn's card", told)
	}
}

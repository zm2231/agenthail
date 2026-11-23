package daemon

import (
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/codextest"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// The item codex-cli 0.163.0 records when the model calls
// request_user_input_async: an agent message delivered "async" that carries
// the questions, under the tool call's id. The tool itself returns
// {"accepted":true} at once and holds nothing open.
func codexAsyncQuestionItem(itemID string) map[string]any {
	return map[string]any{
		"type": "agentMessage", "id": itemID, "phase": "final_answer", "memoryCitation": nil, "delivery": "async",
		"text":      "Which database should the service use?\n- Postgres\n- Keep both",
		"questions": []any{map[string]any{"title": "Which database should the service use?", "options": []any{"Postgres", "Keep both"}}},
	}
}

type codexThreadItems struct {
	mu      sync.Mutex
	entries []any
}

func (i *codexThreadItems) set(turnID string, newestFirst ...map[string]any) {
	i.mu.Lock()
	defer i.mu.Unlock()
	i.entries = nil
	for _, item := range newestFirst {
		i.entries = append(i.entries, map[string]any{"turnId": turnID, "item": item})
	}
}

func (i *codexThreadItems) respond(method string, params map[string]any) map[string]any {
	if method != "thread/items/list" || params["turnId"] != nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return map[string]any{"data": append([]any(nil), i.entries...)}
}

type questionDecision struct {
	SessionID  string `json:"sessionId"`
	Answerable bool   `json:"answerable"`
	Requests   []struct {
		ID        string `json:"id"`
		Kind      string `json:"kind"`
		Questions []struct {
			Question string `json:"question"`
			Options  []struct {
				ID    string `json:"id"`
				Label string `json:"label"`
			} `json:"options"`
		} `json:"questions"`
	} `json:"requests"`
}

func (h *runningDaemon) questionDecisions(sessionID string) []questionDecision {
	status, payload := h.request(http.MethodGet, "/api/v1/decisions", nil)
	var page struct {
		Decisions []questionDecision `json:"decisions"`
	}
	if status != http.StatusOK || json.Unmarshal(payload, &page) != nil {
		return nil
	}
	var mine []questionDecision
	for _, item := range page.Decisions {
		if item.SessionID == sessionID {
			mine = append(mine, item)
		}
	}
	return mine
}

func startCodexQuestionSession(t *testing.T) (*runningDaemon, *codextest.Lifecycle, *codexThreadItems, *registry.Registry, string) {
	t.Helper()
	items := &codexThreadItems{}
	home := codextest.Home(t)
	lifecycle := codextest.InstallLifecycle(t, home, items.respond)
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	h, _ := runDaemon(t, store, codexsurface.NewCodex(unreachableDesktop))
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "session-create", "surface": "codex", "message": "Plan the storage move", "cwd": t.TempDir()})
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.Unmarshal(payload, &created); status != http.StatusCreated || err != nil || created.Session.ID == "" {
		t.Fatalf("create status=%d body=%s err=%v", status, payload, err)
	}
	return h, lifecycle, items, store, created.Session.ID
}

func TestACodexAsyncQuestionNeedsThePersonAndItsAnswerReachesCodexAsAReply(t *testing.T) {
	h, lifecycle, items, store, id := startCodexQuestionSession(t)
	items.set("turn-1", codexAsyncQuestionItem("call_q1"))
	lifecycle.Daemon().CompleteTurn(id, "Which database should the service use?\n- Postgres\n- Keep both")

	var card questionDecision
	testkit.Eventually(t, "the question to be an answerable decision", 10*time.Second, func() bool {
		found := h.questionDecisions(id)
		if len(found) != 1 || !found[0].Answerable || len(found[0].Requests) != 1 {
			return false
		}
		card = found[0]
		return true
	})
	request := card.Requests[0]
	if askRef(store, request.ID) != id+":call_q1" || len(request.Questions) != 1 || request.Questions[0].Question != "Which database should the service use?" ||
		len(request.Questions[0].Options) != 2 || request.Questions[0].Options[1].Label != "Keep both" {
		t.Fatalf("decision %+v", card)
	}
	testkit.Eventually(t, "the row to wait on the person", 10*time.Second, func() bool {
		row := h.catalogSession(id)
		return row != nil && row.Ask != nil && row.Ask.ID == request.ID && row.TurnState != nil && row.TurnState.State == turnWaitingPerson
	})

	starts := len(lifecycle.Daemon().Calls("turn/start"))
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": request.ID, "decision": map[string]any{"choices": []string{request.Questions[0].Options[1].ID}}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	calls := lifecycle.Daemon().Calls("turn/start")
	if len(calls) != starts+1 {
		t.Fatalf("the answer started %d turns, want 1", len(calls)-starts)
	}
	input, _ := calls[len(calls)-1]["input"].([]any)
	first, _ := input[0].(map[string]any)
	want := "<send_user_message_question_reply>\n" +
		`[{"answer":"Keep both","question":"Which database should the service use?","questionItemId":"[\"request_user_input_async\",\"call_q1\",0]"}]` +
		"\n</send_user_message_question_reply>"
	if len(input) != 1 || first["text"] != want {
		t.Fatalf("Codex was sent %v, want the reply %q", input, want)
	}
	testkit.Eventually(t, "the decision to leave the feed", 10*time.Second, func() bool { return len(h.questionDecisions(id)) == 0 })
}

func TestACodexAsyncQuestionAnsweredInCodexLeavesNeedsYou(t *testing.T) {
	h, lifecycle, items, store, id := startCodexQuestionSession(t)
	items.set("turn-1", codexAsyncQuestionItem("call_q1"))
	lifecycle.Daemon().CompleteTurn(id, "Which database should the service use?")
	var askID string
	testkit.Eventually(t, "the question to be a decision", 10*time.Second, func() bool {
		found := h.questionDecisions(id)
		if len(found) != 1 || len(found[0].Requests) != 1 {
			return false
		}
		askID = found[0].Requests[0].ID
		return true
	})

	reply := map[string]any{"type": "userMessage", "id": "user-2", "content": []any{map[string]any{"type": "text",
		"text": "<send_user_message_question_reply>\n" + `[{"answer":"Postgres","question":"Which database should the service use?","questionItemId":"[\"request_user_input_async\",\"call_q1\",0]"}]` + "\n</send_user_message_question_reply>"}}}
	items.set("turn-2", reply, codexAsyncQuestionItem("call_q1"))
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "send", "sessionId": id, "message": "Carry on"})
	if status != http.StatusOK && status != http.StatusAccepted {
		t.Fatalf("send status=%d body=%s", status, payload)
	}
	lifecycle.Daemon().CompleteTurn(id, "Using Postgres.")

	testkit.Eventually(t, "the answered question to leave Needs you", 10*time.Second, func() bool {
		ask, found, err := store.Ask(askID)
		return err == nil && found && ask.State == registry.AskWithdrawn && len(h.questionDecisions(id)) == 0
	})
}

func TestAnAnswerToACodexQuestionAskedMidTurnSteersTheRunningTurn(t *testing.T) {
	h, lifecycle, items, _, id := startCodexQuestionSession(t)
	items.set("turn-1", codexAsyncQuestionItem("call_q1"))
	lifecycle.Daemon().Notify("thread/status/changed", map[string]any{"threadId": id, "status": map[string]any{"type": "active", "activeFlags": []any{}}})
	var card questionDecision
	testkit.Eventually(t, "the question asked mid-turn to be a decision", 10*time.Second, func() bool {
		found := h.questionDecisions(id)
		if len(found) != 1 || len(found[0].Requests) != 1 {
			return false
		}
		card = found[0]
		return true
	})

	starts := len(lifecycle.Daemon().Calls("turn/start"))
	request := card.Requests[0]
	status, payload := h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": request.ID, "decision": map[string]any{"answers": map[string]string{request.Questions[0].Question: "MySQL, for now"}}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	steers := lifecycle.Daemon().Calls("turn/steer")
	if len(steers) != 1 || len(lifecycle.Daemon().Calls("turn/start")) != starts {
		t.Fatalf("the answer made %d steers and %d new turns, want one steer", len(steers), len(lifecycle.Daemon().Calls("turn/start"))-starts)
	}
	input, _ := steers[0]["input"].([]any)
	first, _ := input[0].(map[string]any)
	want := "<send_user_message_question_reply>\n" +
		`[{"answer":"MySQL, for now","question":"Which database should the service use?","questionItemId":"[\"request_user_input_async\",\"call_q1\",0]"}]` +
		"\n</send_user_message_question_reply>"
	if first["text"] != want {
		t.Fatalf("the running turn was steered with %v, want %q", input, want)
	}
}

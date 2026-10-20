package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const pickABranch = `{"questions":[{"question":"Which branch?","header":"Branch","options":[{"label":"main","description":"Release from main"},{"label":"next","description":"Release from next"}],"multiSelect":false}]}`

type withdrawals struct {
	mu          sync.Mutex
	identifiers []string
}

func (w *withdrawals) record(identifier string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.identifiers = append(w.identifiers, identifier)
	return nil
}

func (w *withdrawals) has(identifier string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return slices.Contains(w.identifiers, identifier)
}

func (h *runningDaemon) postPrompt(prompt string, pid int) (int, ClaudePermissionResponse) {
	h.t.Helper()
	body := `{"pid":` + strings.TrimSpace(string(mustJSON(h.t, pid))) + `,"prompt":` + prompt + `}`
	request, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/claude/permission-request", bytes.NewReader([]byte(body)))
	if err != nil {
		h.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		h.t.Fatal(err)
	}
	defer response.Body.Close()
	var decoded ClaudePermissionResponse
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func TestATerminalQuestionHeldByTheHookIsAnsweredFromAgenthailAndItsNoticesAreWithdrawn(t *testing.T) {
	q := newClaudeQuestion(t)
	desktop := &withdrawals{}
	h, _ := runDaemonWith(t, q.store, func(d *Daemon) { d.withdrawDesktop = desktop.record }, q.claude)
	eventually(t, "the question the record shows to be announced", func() bool { return q.pushes.count("session.waiting") == 1 })
	generic := waitingNotificationKey(q.session.ID, questionID)

	prompt := `{"session_id":"conversation-question","tool_name":"AskUserQuestion","tool_input":` + pickABranch + `}`
	type held struct {
		status   int
		response ClaudePermissionResponse
	}
	done := make(chan held, 1)
	go func() {
		status, response := h.postPrompt(prompt, q.process.PID)
		done <- held{status, response}
	}()

	var waiting *surface.Waiting
	eventually(t, "the session to wait on the held prompt", func() bool {
		waiting = h.sessionWaiting(q.session.ID)
		return waiting != nil && strings.Contains(waiting.ID, ":held:")
	})
	eventually(t, "the notice that could not say what was asked to be withdrawn", func() bool {
		return desktop.has("agenthail-"+generic) && slices.ContainsFunc(q.pushes.all(""), func(push map[string]string) bool { return push["collapseId"] == generic })
	})
	eventually(t, "the held prompt to be announced with what it asks", func() bool { return q.pushes.count("session.waiting") == 2 })
	if announced := q.pushes.all("session.waiting")[1]; announced["requestId"] != waiting.ID {
		t.Fatalf("announcement=%+v, want request %s", announced, waiting.ID)
	}

	status, payload := h.request(http.MethodGet, "/api/v1/requests?sessionId="+q.session.ID, nil)
	var state surface.RequestState
	if err := json.Unmarshal(payload, &state); status != http.StatusOK || err != nil || len(state.Requests) != 1 || state.Requests[0].Questions[0].Question != "Which branch?" {
		t.Fatalf("requests status=%d body=%s", status, payload)
	}
	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": q.session.ID, "requestId": waiting.ID, "decision": map[string]any{"behavior": "allow", "answers": map[string]string{"Which branch?": "next"}}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	result := <-done
	if result.status != http.StatusOK || result.response.Decision == nil || result.response.Decision.Behavior != "allow" {
		t.Fatalf("hook got status=%d decision=%+v", result.status, result.response.Decision)
	}
	if answers, _ := result.response.Decision.UpdatedInput["answers"].(map[string]any); answers["Which branch?"] != "next" {
		t.Fatalf("updatedInput=%+v", result.response.Decision.UpdatedInput)
	}

	eventually(t, "the answered question to stop waiting while Claude acts on it", func() bool { return h.sessionWaiting(q.session.ID) == nil })
	eventually(t, "the answered question's notices to be withdrawn", func() bool {
		return desktop.has("agenthail-" + waitingNotificationKey(q.session.ID, waiting.ID))
	})
	for range 3 {
		session := q.session
		if _, err := h.daemon.observeTurn(context.Background(), q.claude, &session); err != nil {
			t.Fatal(err)
		}
	}
	if recorded, _, err := q.store.WaitingState(q.session.ID); err != nil || recorded != nil {
		t.Fatalf("the record still showing the answered question was recorded as a wait: %+v err=%v", recorded, err)
	}
	if count := q.pushes.count("session.waiting"); count != 2 {
		t.Fatalf("the answered question was announced again: %d announcements", count)
	}
}

func TestOnlyAgenthailsOwnHookCanPostAPrompt(t *testing.T) {
	q := newClaudeQuestion(t)
	h, _ := runDaemon(t, q.store, q.claude)
	request, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/claude/permission-request", strings.NewReader(`{"pid":1,"prompt":{}}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Origin", h.server.URL)
	request.AddCookie(&http.Cookie{Name: "agenthail_dashboard", Value: e2eToken})
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("a browser session posted a prompt: status=%d", response.StatusCode)
	}
	if status, _ := h.postPrompt(`{"session_id":"conversation-question","tool_name":"Bash"}`, q.process.PID+100000); status != http.StatusConflict {
		t.Fatalf("a prompt from another process was held: status=%d", status)
	}
}

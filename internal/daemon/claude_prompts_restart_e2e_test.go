package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

// terminalAsker is a terminal Claude session whose hook holds each question
// it asks, posting again to whichever daemon is running, as the real hook
// does when a daemon restarts under it.
type terminalAsker struct {
	t          *testing.T
	process    *claudetest.Fixture
	transcript string
	id         string
	mu         sync.Mutex
	daemon     *runningDaemon
}

func (a *terminalAsker) current() *runningDaemon {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.daemon
}

func (a *terminalAsker) serve(h *runningDaemon) {
	a.mu.Lock()
	a.daemon = h
	a.mu.Unlock()
}

// ask records question n in the transcript and the session record, then
// holds it through the hook until a daemon answers. The channel carries the
// answer the hook returned to Claude.
func (a *terminalAsker) ask(n int) <-chan map[string]any {
	a.t.Helper()
	question := fmt.Sprintf("Question %d: which branch?", n)
	input := fmt.Sprintf(`{"questions":[{"question":%q,"header":"Branch","options":[{"label":"main","description":"Release from main"},{"label":"next","description":"Release from next"}],"multiSelect":false}]}`, question)
	asked := time.Now().UTC()
	appendClaudeRecords(a.t, a.transcript, fmt.Sprintf(`{"type":"assistant","uuid":"ask-%d","sessionId":%q,"timestamp":%q,"message":{"id":"msg-%d","role":"assistant","content":[{"type":"tool_use","id":"toolu_%d","name":"AskUserQuestion","input":%s}]}}`, n, a.id, asked.Format(time.RFC3339Nano), n, n, input))
	a.process.RecordStatus(a.id, a.process.Home, "", "waiting", "input needed", asked)
	prompt := `{"session_id":"` + a.id + `","tool_name":"AskUserQuestion","tool_input":` + input + `}`
	answered := make(chan map[string]any, 1)
	go func() {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			status, response := postHookPrompt(a.current(), prompt, a.process.PID)
			if status == http.StatusOK && response.Decision != nil {
				answers, _ := response.Decision.UpdatedInput["answers"].(map[string]any)
				appendClaudeRecords(a.t, a.transcript, fmt.Sprintf(`{"type":"user","uuid":"answer-%d","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":"answered"}]}}`, n, a.id, time.Now().UTC().Format(time.RFC3339Nano), n))
				a.process.RecordStatus(a.id, a.process.Home, "", "busy", "", time.Now())
				answered <- answers
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
		answered <- nil
	}()
	return answered
}

// postHookPrompt is the hook's call, which a stopping daemon cuts off.
func postHookPrompt(h *runningDaemon, prompt string, pid int) (int, ClaudePermissionResponse) {
	request, err := http.NewRequest(http.MethodPost, h.server.URL+"/api/v1/claude/permission-request", strings.NewReader(`{"pid":`+fmt.Sprint(pid)+`,"prompt":`+prompt+`}`))
	if err != nil {
		return 0, ClaudePermissionResponse{}
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, ClaudePermissionResponse{}
	}
	defer response.Body.Close()
	var decoded ClaudePermissionResponse
	_ = json.NewDecoder(response.Body).Decode(&decoded)
	return response.StatusCode, decoded
}

func (h *runningDaemon) catalogSession(id string) *dashboardSession {
	h.t.Helper()
	status, payload := h.request(http.MethodGet, "/api/v1/snapshot", nil)
	if status != http.StatusOK {
		h.t.Fatalf("snapshot status=%d body=%s", status, payload)
	}
	var decoded struct {
		Sessions []dashboardSession `json:"sessions"`
	}
	if err := json.Unmarshal(payload, &decoded); err != nil {
		h.t.Fatal(err)
	}
	for _, session := range decoded.Sessions {
		if session.ID == id {
			return &session
		}
	}
	return nil
}

// answerHeld waits for the catalog to show question n held and answerable,
// checks that the requests a client fetches for that wait hold it, and
// answers it from Agenthail. It returns the request ID.
func answerHeld(t *testing.T, h *runningDaemon, id string, n int, root string) string {
	t.Helper()
	question := fmt.Sprintf("Question %d: which branch?", n)
	var session *dashboardSession
	eventually(t, fmt.Sprintf("question %d to be held and answerable", n), func() bool {
		session = h.catalogSession(id)
		return session != nil && session.WaitingOn != nil && strings.Contains(session.WaitingOn.ID, ":held:") && session.WaitingOn.Answerable && session.Capabilities.Respond
	})
	if session.ClaudeConfigRoot == nil || session.ClaudeConfigRoot.Root != root {
		t.Fatalf("the session's config root is %+v, want %s", session.ClaudeConfigRoot, root)
	}
	status, payload := h.request(http.MethodGet, "/api/v1/requests?sessionId="+id, nil)
	var state surface.RequestState
	if err := json.Unmarshal(payload, &state); status != http.StatusOK || err != nil || len(state.Requests) != 1 || state.Requests[0].ID != session.WaitingOn.ID || state.Requests[0].Questions[0].Question != question {
		t.Fatalf("requests for wait %s: status=%d body=%s", session.WaitingOn.ID, status, payload)
	}
	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": session.WaitingOn.ID, "decision": map[string]any{"behavior": "allow", "answers": map[string]string{question: "next"}}})
	if status != http.StatusOK {
		t.Fatalf("respond status=%d body=%s", status, payload)
	}
	return session.WaitingOn.ID
}

func TestATerminalSessionInAnotherConfigRootIsAnsweredQuestionAfterQuestionAcrossADaemonRestart(t *testing.T) {
	process := claudetest.New(t)
	root := process.AddRoot("work")
	t.Setenv("HOME", process.Home)
	process.Listen()
	const id = "conversation-in-work"
	transcript := process.Transcript(id, process.Home)
	process.RecordStatus(id, process.Home, "", "idle", "", time.Time{})
	store, err := registry.Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Transport: "uds", Status: surface.StatusIdle, HasLocal: true, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	asker := &terminalAsker{t: t, process: process, transcript: transcript, id: id}
	h, stop := runDaemon(t, store, surfaces.NewClaude(nil, process.Home))
	asker.serve(h)

	var answered []string
	for n := 1; n <= 2; n++ {
		hook := asker.ask(n)
		answered = append(answered, answerHeld(t, h, id, n, root))
		if answers := <-hook; answers[fmt.Sprintf("Question %d: which branch?", n)] != "next" {
			t.Fatalf("Claude got answers %+v for question %d", answers, n)
		}
		eventually(t, fmt.Sprintf("Claude to go on after question %d", n), func() bool { return h.sessionWaiting(id) == nil })
		process.RecordStatus(id, process.Home, "", "idle", "", time.Time{})
	}

	hook := asker.ask(3)
	eventually(t, "question 3 to be held before the restart", func() bool {
		waiting := h.sessionWaiting(id)
		return waiting != nil && strings.Contains(waiting.ID, ":held:")
	})
	before := h.sessionWaiting(id).ID
	// A stopping daemon process takes the hook's connection with it.
	h.server.CloseClientConnections()
	stop()
	restarted, _ := runDaemon(t, store, surfaces.NewClaude(nil, process.Home))
	asker.serve(restarted)
	after := answerHeld(t, restarted, id, 3, root)
	if after == before || slices.Contains(answered, after) {
		t.Fatalf("the restarted daemon held question 3 as %s, an ID already used for %s and %v", after, before, answered)
	}
	if answers := <-hook; answers["Question 3: which branch?"] != "next" {
		t.Fatalf("Claude got answers %+v for question 3 after the restart", answers)
	}
	eventually(t, "Claude to go on after the restart", func() bool { return restarted.sessionWaiting(id) == nil })

	process.RecordStatus(id, process.Home, "", "idle", "", time.Time{})
	hook = asker.ask(4)
	if fourth := answerHeld(t, restarted, id, 4, root); fourth == after || slices.Contains(answered, fourth) {
		t.Fatalf("question 4 reused request %s", fourth)
	}
	if answers := <-hook; answers["Question 4: which branch?"] != "next" {
		t.Fatalf("Claude got answers %+v for question 4", answers)
	}
}

package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
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
	hooks      sync.WaitGroup
	mu         sync.Mutex
	daemon     *runningDaemon
	// connected ends the hook's calls to daemon, as the end of its process
	// does.
	connected context.Context
	cut       context.CancelFunc
	// ended is set once the test is over and Claude has gone.
	ended bool
}

func newTerminalAsker(t *testing.T, process *claudetest.Fixture, transcript, id string) *terminalAsker {
	a := &terminalAsker{t: t, process: process, transcript: transcript, id: id}
	t.Cleanup(func() {
		a.serve(nil)
		a.mu.Lock()
		a.ended = true
		a.mu.Unlock()
		a.hooks.Wait()
	})
	return a
}

// serve points the hook at h, cutting its calls to the daemon before; nil
// leaves it waiting for a daemon to start.
func (a *terminalAsker) serve(h *runningDaemon) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cut != nil {
		a.cut()
	}
	a.daemon, a.connected, a.cut = h, nil, nil
	if h != nil {
		a.connected, a.cut = context.WithCancel(context.Background())
		// Cleanups run last first, so this cuts the hook before h stops.
		a.t.Cleanup(func() { a.serve(nil) })
	}
}

func (a *terminalAsker) current() (*runningDaemon, context.Context, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.daemon, a.connected, a.ended
}

func question(n int) string { return fmt.Sprintf("Question %d: which branch?", n) }

// ask records question n in the transcript and the session record, then
// holds it through the hook until a daemon answers. Like Agenthail's hook,
// it posts again only when the connection fails; any other reply without a
// decision leaves the prompt to Claude's dialog. The channel carries what
// the hook returned to Claude.
func (a *terminalAsker) ask(n int) <-chan hookResult {
	a.t.Helper()
	input := fmt.Sprintf(`{"questions":[{"question":%q,"header":"Branch","options":[{"label":"main","description":"Release from main"},{"label":"next","description":"Release from next"}],"multiSelect":false}]}`, question(n))
	asked := time.Now().UTC()
	appendClaudeRecords(a.t, a.transcript, fmt.Sprintf(`{"type":"assistant","uuid":"ask-%d","sessionId":%q,"timestamp":%q,"message":{"id":"msg-%d","role":"assistant","content":[{"type":"tool_use","id":"toolu_%d","name":"AskUserQuestion","input":%s}]}}`, n, a.id, asked.Format(time.RFC3339Nano), n, n, input))
	a.process.RecordStatus(a.id, a.process.Home, "", "waiting", "input needed", asked)
	prompt := `{"session_id":"` + a.id + `","tool_name":"AskUserQuestion","tool_input":` + input + `}`
	answered := make(chan hookResult, 1)
	a.hooks.Add(1)
	go func() {
		defer a.hooks.Done()
		for {
			h, connected, ended := a.current()
			if ended {
				answered <- hookResult{}
				return
			}
			if h == nil {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			status, body := postHookPrompt(connected, h, prompt, a.process.PID)
			if status == 0 {
				time.Sleep(20 * time.Millisecond)
				continue
			}
			var response ClaudePermissionResponse
			_ = json.Unmarshal(body, &response)
			result := hookResult{status: status, body: string(body)}
			if response.Decision != nil {
				result.answers, _ = response.Decision.UpdatedInput["answers"].(map[string]any)
			}
			answered <- result
			return
		}
	}()
	return answered
}

// goOn records what Claude writes once question n is answered: the tool
// result, and the process working again.
func (a *terminalAsker) goOn(n int) {
	a.t.Helper()
	appendClaudeRecords(a.t, a.transcript, fmt.Sprintf(`{"type":"user","uuid":"answer-%d","sessionId":%q,"timestamp":%q,"message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_%d","content":"answered"}]}}`, n, a.id, time.Now().UTC().Format(time.RFC3339Nano), n))
	a.process.RecordStatus(a.id, a.process.Home, "", "busy", "", time.Now())
}

// hookResult is what the hook got from the daemon; status 0 means the test
// ended first.
type hookResult struct {
	status  int
	body    string
	answers map[string]any
}

func (r hookResult) answered(n int) bool { return r.answers[question(n)] == "next" }

// postHookPrompt is the hook's call; status 0 means the connection failed.
func postHookPrompt(ctx context.Context, h *runningDaemon, prompt string, pid int) (int, []byte) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, h.server.URL+"/api/v1/claude/permission-request", strings.NewReader(`{"pid":`+fmt.Sprint(pid)+`,"prompt":`+prompt+`}`))
	if err != nil {
		return 0, nil
	}
	request.Header.Set("Authorization", "Bearer "+e2eToken)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return 0, nil
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return 0, nil
	}
	return response.StatusCode, body
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

// answerHeld waits for the catalog to show question n held and answerable
// and for the requests a client fetches for that wait to hold it, then
// answers it from Agenthail. It returns the request ID. Just after a
// restart the catalog can still name the last run's hold; clients refetch
// once it names the new one.
func answerHeld(t *testing.T, h *runningDaemon, id string, n int, root string) string {
	t.Helper()
	var session *dashboardSession
	var state surface.RequestState
	eventually(t, fmt.Sprintf("question %d to be held, answerable and listed", n), func() bool {
		session = h.catalogSession(id)
		if session == nil || session.WaitingOn == nil || !strings.Contains(session.WaitingOn.ID, ":held:") || !session.WaitingOn.Answerable || !session.Capabilities.Respond {
			return false
		}
		status, payload := h.request(http.MethodGet, "/api/v1/requests?sessionId="+id, nil)
		state = surface.RequestState{}
		return status == http.StatusOK && json.Unmarshal(payload, &state) == nil && len(state.Requests) == 1 && state.Requests[0].ID == session.WaitingOn.ID
	})
	if session.ClaudeConfigRoot == nil || session.ClaudeConfigRoot.Root != root {
		t.Fatalf("the session's config root is %+v, want %s", session.ClaudeConfigRoot, root)
	}
	if asked := state.Requests[0].Questions; len(asked) != 1 || asked[0].Question != question(n) {
		t.Fatalf("request %s asks %+v, want question %d", session.WaitingOn.ID, asked, n)
	}
	var status int
	var payload []byte
	status, payload = h.request(http.MethodPost, "/api/v1/actions", map[string]any{"action": "respond", "sessionId": id, "requestId": session.WaitingOn.ID, "decision": map[string]any{"behavior": "allow", "answers": map[string]string{question(n): "next"}}})
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
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterSession(surface.Session{ID: id, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: process.PID, Transport: "uds", Status: surface.StatusIdle, HasLocal: true, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	asker := newTerminalAsker(t, process, transcript, id)
	h, stop := runDaemon(t, store, surfaces.NewClaude(nil, process.Home))
	asker.serve(h)

	var answered []string
	for n := 1; n <= 2; n++ {
		hook := asker.ask(n)
		answered = append(answered, answerHeld(t, h, id, n, root))
		if result := <-hook; !result.answered(n) {
			t.Fatalf("question %d: the hook got status=%d body=%s", n, result.status, result.body)
		}
		asker.goOn(n)
		eventually(t, fmt.Sprintf("Claude to go on after question %d", n), func() bool { return h.sessionWaiting(id) == nil })
		process.RecordStatus(id, process.Home, "", "idle", "", time.Time{})
	}

	hook := asker.ask(3)
	eventually(t, "question 3 to be held before the restart", func() bool {
		waiting := h.sessionWaiting(id)
		return waiting != nil && strings.Contains(waiting.ID, ":held:")
	})
	before := h.sessionWaiting(id).ID
	// The stopping daemon process takes the hook's connection with it.
	asker.serve(nil)
	stop()
	restarted, _ := runDaemon(t, store, surfaces.NewClaude(nil, process.Home))
	asker.serve(restarted)
	after := answerHeld(t, restarted, id, 3, root)
	if after == before || slices.Contains(answered, after) {
		t.Fatalf("the restarted daemon held question 3 as %s, an ID already used for %s and %v", after, before, answered)
	}
	if result := <-hook; !result.answered(3) {
		t.Fatalf("question 3 after the restart: the hook got status=%d body=%s", result.status, result.body)
	}
	asker.goOn(3)
	eventually(t, "Claude to go on after the restart", func() bool { return restarted.sessionWaiting(id) == nil })

	process.RecordStatus(id, process.Home, "", "idle", "", time.Time{})
	hook = asker.ask(4)
	if fourth := answerHeld(t, restarted, id, 4, root); fourth == after || slices.Contains(answered, fourth) {
		t.Fatalf("question 4 reused request %s", fourth)
	}
	if result := <-hook; !result.answered(4) {
		t.Fatalf("question 4: the hook got status=%d body=%s", result.status, result.body)
	}
}

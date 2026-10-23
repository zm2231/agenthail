package cli

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

const restartConversation = "conversation-question"

type promptRestart struct {
	t       *testing.T
	claude  *claudetest.Fixture
	store   *registry.Registry
	listen  string
	client  *daemonAPIClient
	decided chan *surfaces.ClaudePromptDecision
}

func newPromptRestart(t *testing.T) (*promptRestart, func()) {
	t.Helper()
	previous := claudePromptHookRetry
	claudePromptHookRetry = 20 * time.Millisecond
	t.Cleanup(func() { claudePromptHookRetry = previous })
	t.Setenv("HOME", t.TempDir())
	claude := claudetest.New(t)
	claude.Listen()
	transcript := claude.Transcript(restartConversation, claude.Home)
	claude.RecordStatus(restartConversation, claude.Home, "session_question", "waiting", "input needed", time.Now())
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	if err := store.RegisterSession(surface.Session{ID: restartConversation, Bridge: "session_question", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, PID: claude.PID, Status: surface.StatusBusy, HasLocal: true, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	h := &promptRestart{t: t, claude: claude, store: store, listen: listener.Addr().String(), decided: make(chan *surfaces.ClaudePromptDecision, 1)}
	listener.Close()
	if err := daemon.SaveDashboardConfig(daemon.DashboardConfig{Enabled: true, Listen: h.listen}); err != nil {
		t.Fatal(err)
	}
	stop := h.serve()
	if h.client, err = newDaemonAPIClient(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	prompt := `{"session_id":"` + restartConversation + `","tool_name":"AskUserQuestion","tool_input":{"questions":[{"question":"Which branch?","header":"Branch","options":[{"label":"main","description":""},{"label":"next","description":""}],"multiSelect":false}]}}`
	go func() { h.decided <- holdClaudePrompt(ctx, h.client, strings.NewReader(prompt), claude.PID) }()
	return h, stop
}

func (h *promptRestart) serve() func() {
	h.t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := daemon.New(h.store, []surface.Surface{surfaces.NewClaude(nil, h.claude.Home)}).Serve(ctx); err != nil {
			h.t.Errorf("serve: %v", err)
		}
	}()
	for deadline := time.Now().Add(10 * time.Second); ; {
		if conn, err := net.Dial("tcp", h.listen); err == nil {
			conn.Close()
			break
		}
		if time.Now().After(deadline) {
			h.t.Fatal("the daemon's dashboard never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	return func() { cancel(); <-done }
}

func (h *promptRestart) heldRequest() string {
	h.t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		body, status, err := h.client.call(context.Background(), http.MethodGet, "/api/v1/requests", url.Values{"sessionId": {restartConversation}}, nil, nil)
		var state surface.RequestState
		if err != nil || status != http.StatusOK || json.Unmarshal(body, &state) != nil {
			continue
		}
		for _, request := range state.Requests {
			if strings.Contains(request.ID, ":held:") {
				return request.ID
			}
		}
	}
	h.t.Fatal("the daemon never held the prompt")
	return ""
}

func (h *promptRestart) stillHeld() {
	h.t.Helper()
	select {
	case decision := <-h.decided:
		h.t.Fatalf("the hook ended with %+v when the daemon holding its prompt stopped", decision)
	case <-time.After(200 * time.Millisecond):
	}
}

func (h *promptRestart) decision() *surfaces.ClaudePromptDecision {
	h.t.Helper()
	select {
	case decision := <-h.decided:
		return decision
	case <-time.After(10 * time.Second):
		h.t.Fatal("the hook never ended")
		return nil
	}
}

func TestAPromptHeldAcrossADaemonRestartIsAnsweredThroughTheNewDaemon(t *testing.T) {
	h, stop := newPromptRestart(t)
	h.heldRequest()
	stop()
	h.stillHeld()

	t.Cleanup(h.serve())
	action := map[string]any{"action": "respond", "sessionId": restartConversation, "requestId": h.heldRequest(), "decision": map[string]any{"behavior": "allow", "answers": map[string]string{"Which branch?": "next"}}}
	if body, status, err := h.client.call(context.Background(), http.MethodPost, "/api/v1/actions", nil, action, nil); err != nil || status != http.StatusOK {
		t.Fatalf("respond status=%d err=%v body=%s", status, err, body)
	}
	decision := h.decision()
	if decision == nil || decision.Behavior != "allow" {
		t.Fatalf("decision=%+v", decision)
	}
	if answers, _ := decision.UpdatedInput["answers"].(map[string]any); answers["Which branch?"] != "next" {
		t.Fatalf("updatedInput=%+v", decision.UpdatedInput)
	}
}

func TestAPromptSettledWhileNoDaemonHeldItIsNotShownForTheSessionsNextPrompt(t *testing.T) {
	h, stop := newPromptRestart(t)
	h.heldRequest()
	stop()
	h.stillHeld()
	h.claude.RecordStatus(restartConversation, h.claude.Home, "session_question", "waiting", "approve Bash", time.Now().Add(5*time.Second))

	t.Cleanup(h.serve())
	if decision := h.decision(); decision != nil {
		t.Fatalf("a settled prompt got decision %+v", decision)
	}
	body, status, err := h.client.call(context.Background(), http.MethodGet, "/api/v1/requests", url.Values{"sessionId": {restartConversation}}, nil, nil)
	if (err != nil && status != http.StatusBadRequest) || strings.Contains(string(body), "Which branch?") {
		t.Fatalf("requests status=%d err=%v body=%s", status, err, body)
	}
}

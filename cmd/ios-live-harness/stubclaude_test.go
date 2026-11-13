package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func TestASessionStartedThroughTheBackgroundLauncherRunsTheHostsClaudeStub(t *testing.T) {
	h := newTestHarness(t)
	host := h.hosts[0]
	stub := filepath.Join(host.dir("bin"), stubClaudeName)
	resolved, err := filepath.EvalSymlinks(stub)
	if err != nil {
		t.Fatal(err)
	}
	if reason := h.scope(host).check(endpoint{kind: "exe", path: resolved}); reason != "" {
		t.Fatalf("the watcher flags the host's own Claude stub %s: %s", resolved, reason)
	}
	cwd := filepath.Join(host.root, "work", "started")
	if err := os.MkdirAll(cwd, 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", host.home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(host.home, ".claude"))
	t.Setenv("AGENTHAIL_CLAUDE_BIN", stub)
	claude := surfaces.NewClaude(nil, host.home)

	session, _, err := claude.BackgroundStarter().StartSession(context.Background(), surface.SessionStartOptions{Cwd: cwd, Name: "From the phone", Message: "Plan the release"})
	if err != nil {
		t.Fatal(err)
	}
	if session.ID == "" || session.Cwd != cwd || session.Name != "From the phone" || session.RuntimeKind != surface.RuntimeClaudeBackground {
		t.Fatalf("started %+v, want a background session in %s named From the phone", session, cwd)
	}
	transcript := filepath.Join(host.home, ".claude", "projects", claudeProjectDirUnsafe.ReplaceAllString(cwd, "-"), session.ID+".jsonl")
	if data, err := os.ReadFile(transcript); err != nil || !strings.Contains(string(data), "Plan the release") {
		t.Fatalf("the started session's transcript is %q (%v), want its first message", data, err)
	}
}

func TestAHostRunningAnotherHostsClaudeStubBreaksItsIsolation(t *testing.T) {
	h, err := newHarness(os.Args[0], 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.cleanup)
	a, b := h.hosts[0], h.hosts[1]
	if a.stub == "" || b.stub == "" || a.stub == b.stub {
		t.Fatalf("stubs %q and %q, want one in each host's bin", a.stub, b.stub)
	}
	if reason := h.scope(a).check(endpoint{kind: "exe", path: a.stub}); reason != "" {
		t.Fatalf("host A running its own stub was flagged: %s", reason)
	}
	if reason := h.scope(a).check(endpoint{kind: "exe", path: b.stub}); reason == "" {
		t.Fatal("host A running host B's stub was not flagged")
	}
}

func TestAPromptRaisedInATurnHoldsAtTheHostUntilItIsAnswered(t *testing.T) {
	h := newTestHarness(t)
	handler := h.controlHandler(context.Background())
	host := h.hosts[0]
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(host.home, ".claude"))
	t.Setenv(claudepeer.SocketDirEnv, host.dir("sockets"))
	claude := surfaces.NewClaude(nil, host.home)
	if err := os.WriteFile(filepath.Join(host.home, ".agenthail", "dashboard.token"), []byte("host-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(host.port))
	if err != nil {
		t.Fatal(err)
	}
	type held struct {
		auth    string
		request map[string]any
		waiting *surface.Waiting
		holdErr error
	}
	arrived, answer := make(chan held, 1), make(chan struct{})
	server := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/claude/permission-request" || r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		var got held
		got.auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &got.request)
		var hook struct {
			Prompt surfaces.ClaudePermissionPrompt `json:"prompt"`
			PID    int                             `json:"pid"`
		}
		json.Unmarshal(body, &hook)
		hook.Prompt.PID = hook.PID
		hold, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		_, got.holdErr = claude.HoldPrompt(hold, hook.Prompt)
		cancel()
		session := listed(t, claude, host.turn.conversation.id)
		if observed, err := claude.Observe(r.Context(), &session); err == nil {
			got.waiting = observed.WaitingOn
		}
		arrived <- got
		<-answer
		w.Write([]byte(`{"decision":{"behavior":"allow"}}`))
	})}
	go server.Serve(listener)
	t.Cleanup(func() { server.Close() })

	if missing := post(handler, "/hosts/0/turn/ask", `{"text":"git push"}`); missing.Code != http.StatusConflict {
		t.Fatalf("a prompt with no open turn answered %d, want 409", missing.Code)
	}
	id := control(t, handler, "/hosts/0/turn/start", `{"text":"Ship the release"}`)
	if asked := control(t, handler, "/hosts/0/turn/ask", `{"text":"git push"}`); asked != id {
		t.Fatalf("the prompt named %q, want the open turn %q", asked, id)
	}
	var got held
	select {
	case got = <-arrived:
	case <-time.After(10 * time.Second):
		t.Fatal("the prompt never reached the host")
	}
	prompt, _ := got.request["prompt"].(map[string]any)
	input, _ := prompt["tool_input"].(map[string]any)
	if got.auth != "Bearer host-token" || prompt["session_id"] != id || prompt["tool_name"] != "Bash" || input["command"] != "git push" || got.request["pid"] != float64(host.turn.process.Process.Pid) {
		t.Fatalf("the host got %q %+v, want the hook's request for the turn's process", got.auth, got.request)
	}
	if errors.Is(got.holdErr, surfaces.ErrPromptNotHeld) {
		t.Fatalf("the host's Claude adapter would refuse to hold the prompt: %v", got.holdErr)
	}
	if got.waiting == nil || got.waiting.Kind != surface.WaitingApproval {
		t.Fatalf("while the prompt was held the session waited on %+v, want an approval", got.waiting)
	}
	if again := post(handler, "/hosts/0/turn/ask", `{"text":"git push"}`); again.Code != http.StatusConflict {
		t.Fatalf("a second prompt while one is held answered %d, want 409", again.Code)
	}
	if status := askStatusOf(t, handler); status.Answered {
		t.Fatalf("the prompt read as answered before the host answered it: %+v", status)
	}

	close(answer)
	deadline := time.Now().Add(10 * time.Second)
	status := askStatusOf(t, handler)
	for !status.Answered && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
		status = askStatusOf(t, handler)
	}
	if !status.Answered || string(status.Decision) != `{"behavior":"allow"}` {
		t.Fatalf("after the host answered, the prompt reads %+v", status)
	}
	session := listed(t, claude, id)
	if session.Status != surface.StatusBusy {
		t.Fatalf("after the answer the turn is %q, want busy again", session.Status)
	}
	host.endTurn()
}

func askStatusOf(t *testing.T, handler http.Handler) askStatus {
	t.Helper()
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/hosts/0/turn/ask", nil))
	var status askStatus
	if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &status) != nil {
		t.Fatalf("reading the prompt answered %d: %s", response.Code, response.Body)
	}
	return status
}

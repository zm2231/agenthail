package acp_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/acp"
	"github.com/zm2231/agenthail/internal/acp/acptest"
)

type recorder struct {
	mu          sync.Mutex
	updates     []acp.Update
	permissions []acp.RequestPermissionParams
	answer      func(acp.RequestPermissionParams) acp.RequestPermissionResult
}

func (r *recorder) Request(_ context.Context, method string, params json.RawMessage) (any, error) {
	if method != "session/request_permission" {
		return nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: "method not found"}
	}
	var p acp.RequestPermissionParams
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, err
	}
	r.mu.Lock()
	r.permissions = append(r.permissions, p)
	answer := r.answer
	r.mu.Unlock()
	return answer(p), nil
}

func (r *recorder) Notification(method string, params json.RawMessage) {
	if method != "session/update" {
		return
	}
	var n acp.SessionNotification
	if json.Unmarshal(params, &n) != nil {
		return
	}
	var u acp.Update
	if json.Unmarshal(n.Update, &u) != nil {
		return
	}
	r.mu.Lock()
	r.updates = append(r.updates, u)
	r.mu.Unlock()
}

func (r *recorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var b strings.Builder
	for _, u := range r.updates {
		if u.SessionUpdate == "agent_message_chunk" {
			b.WriteString(u.Text())
		}
	}
	return b.String()
}

func startAgent(t *testing.T, handler acp.Handler) *acp.Conn {
	t.Helper()
	agentIn, clientOut := io.Pipe()
	clientIn, agentOut := io.Pipe()
	agent := acptest.NewAgent(t.TempDir())
	go agent.Serve(agentIn, agentOut)
	conn := acp.NewConn(clientIn, clientOut, handler)
	conn.Start()
	t.Cleanup(func() { clientOut.Close(); agentOut.Close() })
	var init acp.InitializeResult
	if err := conn.Request(context.Background(), "initialize", acp.InitializeParams{ProtocolVersion: acp.ProtocolVersion}, &init); err != nil {
		t.Fatal(err)
	}
	if !init.AgentCapabilities.LoadSession || !init.AgentCapabilities.Session("close") {
		t.Fatalf("init=%+v", init)
	}
	return conn
}

func newSession(t *testing.T, conn *acp.Conn) string {
	t.Helper()
	var setup acp.SessionSetup
	if err := conn.Request(context.Background(), "session/new", acp.NewSessionParams{Cwd: t.TempDir(), MCPServers: []acp.MCPServer{}}, &setup); err != nil {
		t.Fatal(err)
	}
	if setup.SessionID == "" || setup.Modes == nil || len(setup.ConfigOptions) != 2 {
		t.Fatalf("setup=%+v", setup)
	}
	return setup.SessionID
}

func prompt(conn *acp.Conn, id, text string) (string, error) {
	var result acp.PromptResult
	err := conn.Request(context.Background(), "session/prompt", acp.PromptParams{SessionID: id, Prompt: []acp.ContentBlock{{Type: "text", Text: text}}}, &result)
	return result.StopReason, err
}

func TestPromptStreamsUpdatesBeforeItsResponse(t *testing.T) {
	r := &recorder{}
	conn := startAgent(t, r)
	id := newSession(t, conn)
	stop, err := prompt(conn, id, "reply:hello-there")
	if err != nil || stop != acp.StopEndTurn {
		t.Fatalf("stop=%q err=%v", stop, err)
	}
	if got := r.text(); got != "hello-there" {
		t.Fatalf("text=%q", got)
	}
}

func TestPermissionRequestIsServedAndHonoured(t *testing.T) {
	r := &recorder{answer: func(p acp.RequestPermissionParams) acp.RequestPermissionResult {
		for _, option := range p.Options {
			if option.Kind == acp.OptionRejectOnce {
				return acp.Selected(option.OptionID)
			}
		}
		return acp.Cancelled()
	}}
	conn := startAgent(t, r)
	id := newSession(t, conn)
	stop, err := prompt(conn, id, "ask")
	if err != nil || stop != acp.StopEndTurn || r.text() != "rejected" {
		t.Fatalf("stop=%q err=%v text=%q", stop, err, r.text())
	}
	if len(r.permissions) != 1 || r.permissions[0].ToolCall.ToolCallID != "call-1" || len(r.permissions[0].Options) != 4 {
		t.Fatalf("permissions=%+v", r.permissions)
	}
}

func TestCancelEndsASlowTurn(t *testing.T) {
	conn := startAgent(t, &recorder{})
	id := newSession(t, conn)
	done := make(chan string, 1)
	go func() {
		stop, _ := prompt(conn, id, "slow")
		done <- stop
	}()
	time.Sleep(100 * time.Millisecond)
	if err := conn.Notify("session/cancel", acp.SessionIDParams{SessionID: id}); err != nil {
		t.Fatal(err)
	}
	select {
	case stop := <-done:
		if stop != acp.StopCancelled {
			t.Fatalf("stop=%q", stop)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel did not end the turn")
	}
}

func TestUnknownClientMethodIsRefused(t *testing.T) {
	conn := startAgent(t, nil)
	id := newSession(t, conn)
	stop, err := prompt(conn, id, "ask")
	if err != nil || stop != acp.StopCancelled {
		t.Fatalf("an unserved permission request must fail the turn's tool: stop=%q err=%v", stop, err)
	}
}

func TestAgentErrorsAndClosedConnections(t *testing.T) {
	conn := startAgent(t, &recorder{})
	err := conn.Request(context.Background(), "session/prompt", acp.PromptParams{SessionID: "missing"}, nil)
	var agentErr *acp.Error
	if !errors.As(err, &agentErr) || agentErr.Code != acp.CodeInvalidParams {
		t.Fatalf("err=%v", err)
	}

	reader, writer := io.Pipe()
	closing := acp.NewConn(reader, io.Discard, nil)
	closing.Start()
	pending := make(chan error, 1)
	go func() { pending <- closing.Request(context.Background(), "session/new", acp.NewSessionParams{}, nil) }()
	time.Sleep(50 * time.Millisecond)
	writer.Close()
	select {
	case err := <-pending:
		if !errors.Is(err, acp.ErrClosed) {
			t.Fatalf("err=%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the agent's output did not end the pending request")
	}
	if err := closing.Notify("session/cancel", acp.SessionIDParams{}); !errors.Is(err, acp.ErrClosed) {
		t.Fatalf("notify after close err=%v", err)
	}
}

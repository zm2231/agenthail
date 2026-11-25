package acptest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/acp"
)

const (
	envRun   = "AGENTHAIL_ACPTEST_AGENT"
	envState = "AGENTHAIL_ACPTEST_STATE"
)

func RunIfAgent() {
	if os.Getenv(envRun) != "1" {
		return
	}
	NewAgent(os.Getenv(envState)).Serve(os.Stdin, os.Stdout)
	os.Exit(0)
}

func Command(stateDir string) ([]string, map[string]string) {
	return []string{os.Args[0]}, map[string]string{envRun: "1", envState: stateDir}
}

type session struct {
	ID      string   `json:"id"`
	Cwd     string   `json:"cwd"`
	Mode    string   `json:"mode"`
	Model   string   `json:"model"`
	History []string `json:"history"`
}

type Agent struct {
	stateDir string
	conn     *acp.Conn
	mu       sync.Mutex
	cancels  map[string]chan struct{}
	early    map[string]bool
}

func NewAgent(stateDir string) *Agent { return &Agent{stateDir: stateDir} }

func (a *Agent) Serve(r io.Reader, w io.Writer) {
	a.cancels, a.early = map[string]chan struct{}{}, map[string]bool{}
	a.conn = acp.NewConn(r, w, a)
	a.conn.Start()
	<-a.conn.Done()
}

func (a *Agent) Notification(method string, params json.RawMessage) {
	if method != "session/cancel" {
		return
	}
	var p acp.SessionIDParams
	_ = json.Unmarshal(params, &p)
	a.mu.Lock()
	if cancel := a.cancels[p.SessionID]; cancel != nil {
		close(cancel)
		delete(a.cancels, p.SessionID)
	} else {
		a.early[p.SessionID] = true
	}
	a.mu.Unlock()
}

func (a *Agent) Request(ctx context.Context, method string, params json.RawMessage) (any, error) {
	switch method {
	case "initialize":
		return map[string]any{
			"protocolVersion":   acp.ProtocolVersion,
			"agentCapabilities": map[string]any{"loadSession": true, "mcpCapabilities": map[string]any{"http": true}, "sessionCapabilities": map[string]any{"close": map[string]any{}, "list": map[string]any{}}},
			"agentInfo":         map[string]any{"name": "fake", "title": "Fake agent", "version": "1"},
		}, nil
	case "session/new":
		var p acp.NewSessionParams
		_ = json.Unmarshal(params, &p)
		s := &session{ID: fmt.Sprintf("fake-%d", time.Now().UnixNano()), Cwd: p.Cwd, Mode: "default", Model: "small"}
		if err := a.save(s); err != nil {
			return nil, err
		}
		return a.setup(s, true), nil
	case "session/load":
		var p acp.LoadSessionParams
		_ = json.Unmarshal(params, &p)
		s, err := a.load(p.SessionID)
		if err != nil {
			return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: err.Error()}
		}
		for i, text := range s.History {
			kind := "user_message_chunk"
			if i%2 == 1 {
				kind = "agent_message_chunk"
			}
			a.update(s.ID, map[string]any{"sessionUpdate": kind, "content": text})
		}
		return a.setup(s, false), nil
	case "session/set_config_option":
		var p acp.SetConfigOptionParams
		_ = json.Unmarshal(params, &p)
		s, err := a.load(p.SessionID)
		if err != nil {
			return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: err.Error()}
		}
		switch {
		case p.ConfigID == "mode" && valid(p.Value, "ask", "default", "bypass"):
			s.Mode = p.Value
		case p.ConfigID == "model" && valid(p.Value, "small", "large"):
			s.Model = p.Value
		default:
			return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: "unknown option value"}
		}
		if err := a.save(s); err != nil {
			return nil, err
		}
		return map[string]any{"configOptions": configOptions(s)}, nil
	case "session/close":
		return map[string]any{}, nil
	case "session/prompt":
		var p acp.PromptParams
		_ = json.Unmarshal(params, &p)
		return a.prompt(ctx, p)
	}
	return nil, &acp.Error{Code: acp.CodeMethodNotFound, Message: "method not found"}
}

func valid(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func (a *Agent) setup(s *session, withID bool) map[string]any {
	result := map[string]any{
		"modes":         map[string]any{"currentModeId": s.Mode, "availableModes": []map[string]string{{"id": "ask", "name": "Ask"}, {"id": "default", "name": "Default"}, {"id": "bypass", "name": "Bypass"}}},
		"configOptions": configOptions(s),
	}
	if withID {
		result["sessionId"] = s.ID
	}
	return result
}

func configOptions(s *session) []map[string]any {
	return []map[string]any{
		{"id": "mode", "name": "Mode", "category": "mode", "type": "select", "currentValue": s.Mode, "options": []map[string]string{{"value": "ask", "name": "Ask"}, {"value": "default", "name": "Default"}, {"value": "bypass", "name": "Bypass"}}},
		{"id": "model", "name": "Model", "category": "model", "type": "select", "currentValue": s.Model, "options": []map[string]string{{"value": "small", "name": "Small"}, {"value": "large", "name": "Large"}}},
	}
}

func (a *Agent) update(sessionID string, update map[string]any) {
	if content, ok := update["content"].(string); ok {
		update["content"] = map[string]string{"type": "text", "text": content}
	}
	_ = a.conn.Notify("session/update", map[string]any{"sessionId": sessionID, "update": update})
}

func (a *Agent) prompt(ctx context.Context, p acp.PromptParams) (any, error) {
	s, err := a.load(p.SessionID)
	if err != nil {
		return nil, &acp.Error{Code: acp.CodeInvalidParams, Message: err.Error()}
	}
	text := ""
	for _, block := range p.Prompt {
		text += block.Text
	}
	cancel := make(chan struct{})
	a.mu.Lock()
	if a.early[s.ID] {
		delete(a.early, s.ID)
		close(cancel)
	} else {
		a.cancels[s.ID] = cancel
	}
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if a.cancels[s.ID] == cancel {
			delete(a.cancels, s.ID)
		}
		a.mu.Unlock()
	}()
	reply, stop := a.run(ctx, s, text, cancel)
	if stop == acp.StopCancelled {
		return acp.PromptResult{StopReason: stop}, nil
	}
	if reply != "" {
		for _, chunk := range chunks(reply) {
			a.update(s.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": chunk})
		}
	}
	s.History = append(s.History, text, reply)
	if err := a.save(s); err != nil {
		return nil, err
	}
	return acp.PromptResult{StopReason: stop}, nil
}

func (a *Agent) run(ctx context.Context, s *session, text string, cancel chan struct{}) (string, string) {
	word := func(prefix string) string {
		for _, field := range strings.Fields(text) {
			if strings.HasPrefix(field, prefix) {
				return strings.TrimPrefix(field, prefix)
			}
		}
		return ""
	}
	if strings.Contains(text, "crash") {
		a.update(s.ID, map[string]any{"sessionUpdate": "agent_message_chunk", "content": "about to crash"})
		os.Exit(3)
	}
	if strings.Contains(text, "think") {
		a.update(s.ID, map[string]any{"sessionUpdate": "agent_thought_chunk", "content": "thinking it over"})
	}
	if strings.Contains(text, "plan") {
		a.update(s.ID, map[string]any{"sessionUpdate": "plan", "entries": []map[string]string{{"content": "first step", "status": "completed", "priority": "high"}, {"content": "second step", "status": "pending", "priority": "low"}}})
	}
	if mode := word("mode:"); mode != "" {
		s.Mode = mode
		a.update(s.ID, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": mode})
	}
	if strings.Contains(text, "tool") || strings.Contains(text, "ask") || strings.Contains(text, "noallow") {
		a.update(s.ID, map[string]any{"sessionUpdate": "tool_call", "toolCallId": "call-1", "title": "Ran echo", "kind": "execute", "status": "pending", "rawInput": map[string]string{"command": "echo hi"}})
		if strings.Contains(text, "ask") || strings.Contains(text, "noallow") {
			options := []acp.PermissionOption{{OptionID: "allow", Name: "Allow", Kind: acp.OptionAllowOnce}, {OptionID: "always", Name: "Always allow", Kind: acp.OptionAllowAlways}, {OptionID: "switch_bypass", Name: "Switch to bypass", Kind: acp.OptionAllowAlways}, {OptionID: "reject", Name: "Reject", Kind: acp.OptionRejectOnce}}
			if strings.Contains(text, "noallow") {
				options = []acp.PermissionOption{{OptionID: "reject", Name: "Reject", Kind: acp.OptionRejectOnce}}
			}
			var result acp.RequestPermissionResult
			if err := a.conn.Request(ctx, "session/request_permission", acp.RequestPermissionParams{SessionID: s.ID, ToolCall: acp.PermissionToolCall{ToolCallID: "call-1"}, Options: options}, &result); err != nil {
				return "", acp.StopCancelled
			}
			switch {
			case result.Outcome.Outcome == "cancelled":
				a.update(s.ID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "failed"})
				return "", acp.StopCancelled
			case result.Outcome.OptionID == "switch_bypass":
				s.Mode = "bypass"
				a.update(s.ID, map[string]any{"sessionUpdate": "current_mode_update", "currentModeId": "bypass"})
			case result.Outcome.OptionID == "reject":
				a.update(s.ID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "failed"})
				return "rejected", acp.StopEndTurn
			}
		}
		a.update(s.ID, map[string]any{"sessionUpdate": "tool_call_update", "toolCallId": "call-1", "status": "completed", "content": []map[string]any{{"type": "content", "content": map[string]string{"type": "text", "text": "hi"}}}})
		if strings.Contains(text, "ask") {
			return "allowed", acp.StopEndTurn
		}
	}
	if strings.Contains(text, "slow") {
		select {
		case <-cancel:
			return "", acp.StopCancelled
		case <-time.After(30 * time.Second):
		}
	}
	if stop := word("stop:"); stop != "" {
		return "stopped", stop
	}
	if remembered := word("remember:"); remembered != "" {
		return "OK", acp.StopEndTurn
	}
	if strings.Contains(text, "recall") {
		for _, earlier := range s.History {
			for _, field := range strings.Fields(earlier) {
				if value, ok := strings.CutPrefix(field, "remember:"); ok {
					return value, acp.StopEndTurn
				}
			}
		}
		return "nothing", acp.StopEndTurn
	}
	if reply := word("reply:"); reply != "" {
		return reply, acp.StopEndTurn
	}
	return "done", acp.StopEndTurn
}

func chunks(text string) []string {
	if len(text) < 4 {
		return []string{text}
	}
	half := len(text) / 2
	return []string{text[:half], text[half:]}
}

func (a *Agent) path(id string) string { return filepath.Join(a.stateDir, id+".json") }

func (a *Agent) save(s *session) error {
	data, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return os.WriteFile(a.path(s.ID), data, 0o600)
}

func (a *Agent) load(id string) (*session, error) {
	data, err := os.ReadFile(a.path(id))
	if err != nil {
		return nil, fmt.Errorf("no session %s", id)
	}
	var s session
	return &s, json.Unmarshal(data, &s)
}

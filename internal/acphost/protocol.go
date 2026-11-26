package acphost

import (
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/acp"
)

const Protocol = 1

type Call struct {
	Method string          `json:"method"`
	Key    string          `json:"key,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
}

type Reply struct {
	Result json.RawMessage `json:"result,omitempty"`
	Error  *CallError      `json:"error,omitempty"`
}

type CallError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *CallError) Error() string { return e.Message }

const (
	CodeNotFound    = "not_found"
	CodeBusy        = "busy"
	CodeStale       = "stale"
	CodeDraining    = "draining"
	CodeInterrupted = "interrupted"
	CodeInvalid     = "invalid"
	CodeHeld        = "held_elsewhere"
	CodeFailed      = "failed"
)

func callError(code, format string, args ...any) *CallError {
	return &CallError{Code: code, Message: fmt.Sprintf(format, args...)}
}

type Hello struct {
	Protocol int    `json:"protocol"`
	Version  string `json:"version"`
	PID      int    `json:"pid"`
	Draining bool   `json:"draining"`
}

type CreateParams struct {
	SessionID  string          `json:"sessionId"`
	Agent      string          `json:"agent"`
	Cwd        string          `json:"cwd"`
	Mode       string          `json:"mode,omitempty"`
	Model      string          `json:"model,omitempty"`
	Ceiling    string          `json:"ceiling,omitempty"`
	MCPServers []acp.MCPServer `json:"mcpServers,omitempty"`
}

type SendParams struct {
	SessionID  string          `json:"sessionId"`
	Text       string          `json:"text"`
	MessageID  string          `json:"messageId,omitempty"`
	MCPServers []acp.MCPServer `json:"mcpServers,omitempty"`
}

type SendResult struct {
	TurnID string `json:"turnId"`
}

type SessionParams struct {
	SessionID string `json:"sessionId"`
}

type RespondParams struct {
	SessionID string `json:"sessionId"`
	RequestID string `json:"requestId"`
	Choice    string `json:"choice"`
}

type RespondResult struct {
	Outcome string `json:"outcome"`
}

type OptionParams struct {
	SessionID string `json:"sessionId"`
	Category  string `json:"category"`
	Value     string `json:"value"`
}

type OutcomeParams struct {
	Key string `json:"key"`
}

type Outcome struct {
	State  string          `json:"state"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *CallError      `json:"error,omitempty"`
}

const (
	OutcomeStarted = "started"
	OutcomeDone    = "done"
	OutcomeFailed  = "failed"
)

const (
	StatusIdle    = "idle"
	StatusBusy    = "busy"
	StatusStopped = "stopped"
)

type Request struct {
	ID         string                 `json:"id"`
	ToolCallID string                 `json:"toolCallId"`
	Title      string                 `json:"title,omitempty"`
	Kind       string                 `json:"kind,omitempty"`
	RawInput   json.RawMessage        `json:"rawInput,omitempty"`
	Diffs      []acp.ToolCallContent  `json:"diffs,omitempty"`
	Options    []acp.PermissionOption `json:"options"`
	CreatedAt  time.Time              `json:"createdAt"`
}

type State struct {
	SessionID      string             `json:"sessionId"`
	Agent          string             `json:"agent"`
	AgentSessionID string             `json:"agentSessionId,omitempty"`
	Cwd            string             `json:"cwd"`
	CreatedAt      time.Time          `json:"createdAt"`
	Ceiling        string             `json:"ceiling,omitempty"`
	Status         string             `json:"status"`
	TurnID         string             `json:"turnId,omitempty"`
	LastTurnID     string             `json:"lastTurnId,omitempty"`
	LastStop       string             `json:"lastStop,omitempty"`
	LastActive     time.Time          `json:"lastActive"`
	Title          string             `json:"title,omitempty"`
	Mode           string             `json:"mode,omitempty"`
	Model          string             `json:"model,omitempty"`
	ConfigOptions  []acp.ConfigOption `json:"configOptions,omitempty"`
	Modes          []acp.Mode         `json:"modes,omitempty"`
	Commands       []json.RawMessage  `json:"commands,omitempty"`
	ContextUsed    int64              `json:"contextUsed,omitempty"`
	ContextSize    int64              `json:"contextSize,omitempty"`
	Requests       []Request          `json:"requests,omitempty"`
	Refused        string             `json:"refused,omitempty"`
	PID            int                `json:"pid,omitempty"`
	PIDStart       string             `json:"pidStart,omitempty"`
}

type HeldResult struct {
	Sessions []State `json:"sessions"`
}

type Event struct {
	SessionID string `json:"sessionId"`
	Status    string `json:"status"`
}

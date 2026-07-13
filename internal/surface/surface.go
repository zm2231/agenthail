package surface

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type SurfaceKind string

const (
	KindClaude SurfaceKind = "claude"
	KindCodex  SurfaceKind = "codex"
	KindNotion SurfaceKind = "notion"
)

type SessionStatus string

const (
	StatusIdle    SessionStatus = "idle"
	StatusBusy    SessionStatus = "busy"
	StatusOffline SessionStatus = "offline"
	StatusUnknown SessionStatus = "unknown"
)

type Session struct {
	ID         string        `json:"id"`
	Surface    SurfaceKind   `json:"surface"`
	Name       string        `json:"name"`
	Cwd        string        `json:"cwd"`
	PID        int           `json:"pid"`
	Status     SessionStatus `json:"status"`
	Transcript string        `json:"transcript"`
	HasLocal   bool          `json:"hasLocal"`
	Source     string        `json:"source,omitempty"`
	Transport  string        `json:"transport,omitempty"`
	LastActive time.Time     `json:"lastActive"`
}

func IsReadOnlySession(session *Session) bool {
	if session == nil || session.Surface != KindCodex {
		return false
	}
	return session.Transport == "readOnly" || session.Transport == ""
}

func ReadOnlySessionReason(session *Session) string {
	if !IsReadOnlySession(session) {
		return ""
	}
	if session.Source == "vscode" {
		return "Codex Desktop is not bridged; run 'agenthail launch codex'"
	}
	if session.Source == "cli" || session.Transport == "readOnly" {
		return "Codex terminal session is read only; start a writable session with 'agenthail codex'"
	}
	return "Codex session ownership is unknown; open it in Codex Desktop to make it writable"
}

type SendResult struct {
	UUID     string `json:"uuid"`
	Accepted bool   `json:"accepted"`
}

type DeliveryOutcomeUnknownError struct {
	Err error
}

func (e DeliveryOutcomeUnknownError) Error() string {
	return fmt.Sprintf("delivery outcome is unknown: %v", e.Err)
}

func (e DeliveryOutcomeUnknownError) Unwrap() error { return e.Err }

func DeliveryOutcomeUnknown(err error) error {
	if err == nil {
		return nil
	}
	return DeliveryOutcomeUnknownError{Err: err}
}

func IsDeliveryOutcomeUnknown(err error) bool {
	var target DeliveryOutcomeUnknownError
	return errors.As(err, &target)
}

type SendOptions struct {
	Model string `json:"model,omitempty"`
}

type SessionStartOptions struct {
	Message        string `json:"message"`
	Cwd            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
}

type SessionStarter interface {
	StartSession(ctx context.Context, options SessionStartOptions) (*Session, *SendResult, error)
}

type OptionSender interface {
	SendWithOptions(ctx context.Context, sess *Session, message string, options SendOptions) (*SendResult, error)
}

type ModelOption struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	Default     bool   `json:"default,omitempty"`
}

type ModelLister interface {
	Models(ctx context.Context) ([]ModelOption, error)
}

type HealthChecker interface {
	Health(ctx context.Context) error
}

type RuntimeStatus struct {
	Name        string `json:"name"`
	Reachable   bool   `json:"reachable"`
	Durable     bool   `json:"durable"`
	Backend     string `json:"backend,omitempty"`
	Detail      string `json:"detail,omitempty"`
	Remediation string `json:"remediation,omitempty"`
}

type RuntimeStatusProvider interface {
	RuntimeStatus(ctx context.Context) RuntimeStatus
}

type RuntimeEnsurer interface {
	EnsureRuntime(ctx context.Context) error
}

type TurnObservation struct {
	Status          SessionStatus `json:"status"`
	ActiveTurnID    string        `json:"activeTurnId,omitempty"`
	TerminalTurnID  string        `json:"terminalTurnId,omitempty"`
	CompletedTurnID string        `json:"completedTurnId,omitempty"`
	Reply           *ReplyResult  `json:"reply,omitempty"`
}

type ReplyResult struct {
	Text     string `json:"text"`
	UserText string `json:"userText"` // last user message (for context)
	Done     bool   `json:"done"`
	Error    string `json:"error"`
}

type Exchange struct {
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	Timestamp time.Time `json:"timestamp"`
}

type GoalState struct {
	Objective string `json:"objective"`
	Status    string `json:"status"` // "active", "complete", ""
}

type StreamEvent struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type Capabilities struct {
	Send      bool `json:"send"`
	Stream    bool `json:"stream"`
	Reply     bool `json:"reply"`
	Goal      bool `json:"goal"`
	Compact   bool `json:"compact"`
	Model     bool `json:"model"`
	Interrupt bool `json:"interrupt"`
	Steer     bool `json:"steer"`
}

type Surface interface {
	Name() SurfaceKind
	List(ctx context.Context) ([]Session, error)
	Resolve(ctx context.Context, target string) (*Session, error)
	Observe(ctx context.Context, sess *Session) (*TurnObservation, error)
	Send(ctx context.Context, sess *Session, message string) (*SendResult, error)
	Reply(ctx context.Context, sess *Session, limit int) (*ReplyResult, error)
	Tail(ctx context.Context, sess *Session, n int) ([]Exchange, error)
	Stream(ctx context.Context, sess *Session, uuid string, onEvent func(StreamEvent), timeout time.Duration) error
	GoalSet(ctx context.Context, sess *Session, text string) error
	GoalClear(ctx context.Context, sess *Session) error
	GoalGet(ctx context.Context, sess *Session) (*GoalState, error)
	Compact(ctx context.Context, sess *Session) error
	Model(ctx context.Context, sess *Session, name string) (string, error)
	Interrupt(ctx context.Context, sess *Session) error
	Steer(ctx context.Context, sess *Session, message string) error
	Capabilities() Capabilities
}

func DeriveName(explicit, preview string, maxLen int) string {
	if explicit != "" {
		return truncate(explicit, maxLen)
	}
	return firstLine(preview, maxLen)
}

func firstLine(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	s = strings.TrimSpace(s)
	return truncate(s, maxLen)
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n])
}

func TruncateString(s string, n int) string { return truncate(s, n) }

var ErrUnsupported = errUnsupported{}

type errUnsupported struct{}

func (errUnsupported) Error() string { return "operation not supported by this surface" }
func (errUnsupported) Is(target error) bool {
	_, ok := target.(errUnsupported)
	return ok
}

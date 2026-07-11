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
	StatusIdle       SessionStatus = "idle"
	StatusBusy       SessionStatus = "busy"
	StatusNeedsInput SessionStatus = "needs_input"
	StatusOffline    SessionStatus = "offline"
	StatusUnknown    SessionStatus = "unknown"
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
	LastActive time.Time     `json:"lastActive"`
}

type SendResult struct {
	UUID     string `json:"uuid"`
	ReplyID  string `json:"replyId,omitempty"`
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
	if DeliveryClassOfWithoutUnknown(err) == DeliveryUnknown {
		return true
	}
	var target DeliveryOutcomeUnknownError
	return errors.As(err, &target)
}

func DeliveryClassOfWithoutUnknown(err error) DeliveryClass {
	var classified DeliveryError
	if errors.As(err, &classified) {
		return classified.Class
	}
	return ""
}

type DeliveryClass string

const (
	DeliveryTerminal  DeliveryClass = "terminal"
	DeliveryRetryable DeliveryClass = "retryable"
	DeliveryUnknown   DeliveryClass = "unknown"
)

type DeliveryError struct {
	Class  DeliveryClass
	Status int
	Err    error
}

func (e DeliveryError) Error() string {
	if e.Status != 0 {
		return fmt.Sprintf("delivery %s (HTTP %d): %v", e.Class, e.Status, e.Err)
	}
	return fmt.Sprintf("delivery %s: %v", e.Class, e.Err)
}

func (e DeliveryError) Unwrap() error { return e.Err }

func ClassifyDelivery(class DeliveryClass, status int, err error) error {
	if err == nil {
		return nil
	}
	return DeliveryError{Class: class, Status: status, Err: err}
}

func DeliveryClassOf(err error) DeliveryClass {
	if class := DeliveryClassOfWithoutUnknown(err); class != "" {
		return class
	}
	if IsDeliveryOutcomeUnknown(err) {
		return DeliveryUnknown
	}
	return DeliveryRetryable
}

func IsDeliveryTerminal(err error) bool { return DeliveryClassOf(err) == DeliveryTerminal }

type SendOptions struct {
	Model string `json:"model,omitempty"`
}

type OptionSender interface {
	SendWithOptions(ctx context.Context, sess *Session, message string, options SendOptions) (*SendResult, error)
}

type HealthChecker interface {
	Health(ctx context.Context) error
}

type TurnObservation struct {
	Status          SessionStatus `json:"status"`
	TerminalReason  string        `json:"terminalReason,omitempty"`
	ActiveTurnID    string        `json:"activeTurnId,omitempty"`
	TerminalTurnID  string        `json:"terminalTurnId,omitempty"`
	CompletedTurnID string        `json:"completedTurnId,omitempty"`
	CompletedUserID string        `json:"completedUserId,omitempty"`
	Completions     []Completion  `json:"completions,omitempty"`
	Reply           *ReplyResult  `json:"reply,omitempty"`
}

type Completion struct {
	ID     string       `json:"id"`
	UserID string       `json:"userId,omitempty"`
	Reply  *ReplyResult `json:"reply,omitempty"`
}

type ReplyResult struct {
	Text     string `json:"text"`
	UserText string `json:"userText"` // last user message (for context)
	UserID   string `json:"userId,omitempty"`
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
	Fork      bool `json:"fork"`
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

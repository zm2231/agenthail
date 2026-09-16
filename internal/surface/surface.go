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
	KindZen    SurfaceKind = "zen"
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

type SessionSearchResult struct {
	Session Session `json:"session"`
	Snippet string  `json:"snippet,omitempty"`
}

type SessionSearcher interface {
	SearchSessions(ctx context.Context, query string, limit int) ([]SessionSearchResult, error)
}

type ReadinessChecker interface {
	Ready(ctx context.Context) error
}

func IsReadOnlySession(session *Session) bool {
	if session == nil {
		return false
	}
	if session.Surface == KindZen {
		return strings.TrimSpace(session.Transport) == ""
	}
	if session.Surface != KindCodex {
		return false
	}
	return session.Transport == "readOnly" || session.Transport == ""
}

func ReadOnlySessionReason(session *Session) string {
	if !IsReadOnlySession(session) {
		return ""
	}
	if session.Surface == KindZen {
		return "ZEN workflow sessions stream through Agenthail but have no live control lease"
	}
	if session.Source == "vscode" {
		return "Codex Desktop is not available through Agenthail's Desktop bridge; quit Codex and run 'agenthail launch codex'"
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

type DeliveryUnavailableError struct {
	Err error
}

func (e DeliveryUnavailableError) Error() string {
	return fmt.Sprintf("delivery did not start: %v", e.Err)
}

func (e DeliveryUnavailableError) Unwrap() error { return e.Err }

func DeliveryUnavailable(err error) error {
	if err == nil {
		return nil
	}
	return DeliveryUnavailableError{Err: err}
}

func IsDeliveryUnavailable(err error) bool {
	var target DeliveryUnavailableError
	return errors.As(err, &target)
}

type DeliveryTerminalKind string

const (
	DeliveryTargetMissing        DeliveryTerminalKind = "target_missing"
	DeliveryAuthenticationNeeded DeliveryTerminalKind = "authentication_needed"
	DeliveryAccessDenied         DeliveryTerminalKind = "access_denied"
	DeliveryInvalidRequest       DeliveryTerminalKind = "invalid_request"
	DeliveryOwnershipConflict    DeliveryTerminalKind = "ownership_conflict"
)

type DeliveryTerminalError struct {
	Err  error
	Kind DeliveryTerminalKind
}

func (e DeliveryTerminalError) Error() string {
	if e.Kind != "" {
		return fmt.Sprintf("delivery rejected [%s]: %v", deliveryTerminalLabel(e.Kind), e.Err)
	}
	return fmt.Sprintf("delivery rejected: %v", e.Err)
}

func deliveryTerminalLabel(kind DeliveryTerminalKind) string {
	switch kind {
	case DeliveryTargetMissing:
		return "target missing"
	case DeliveryAuthenticationNeeded:
		return "authentication needed"
	case DeliveryAccessDenied:
		return "access denied"
	case DeliveryInvalidRequest:
		return "invalid request"
	case DeliveryOwnershipConflict:
		return "session ownership conflict"
	default:
		return "delivery rejected"
	}
}

func (e DeliveryTerminalError) Unwrap() error { return e.Err }

func DeliveryTerminal(err error, kinds ...DeliveryTerminalKind) error {
	if err == nil {
		return nil
	}
	var kind DeliveryTerminalKind
	if len(kinds) > 0 {
		kind = kinds[0]
	}
	return DeliveryTerminalError{Err: err, Kind: kind}
}

func IsDeliveryTerminal(err error) bool {
	var target DeliveryTerminalError
	return errors.As(err, &target)
}

func DeliveryTerminalReason(err error) DeliveryTerminalKind {
	var target DeliveryTerminalError
	if errors.As(err, &target) {
		return target.Kind
	}
	return ""
}

type SendOptions struct {
	TurnOptions
	Model           string `json:"model,omitempty"`
	SourceSessionID string `json:"sourceSessionId,omitempty"`
}

type sourceSessionIDContextKey struct{}

func WithSourceSessionID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, sourceSessionIDContextKey{}, id)
}

func SourceSessionID(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	id, _ := ctx.Value(sourceSessionIDContextKey{}).(string)
	return id
}

type SessionStartOptions struct {
	TurnOptions
	Name           string `json:"name,omitempty"`
	Worktree       string `json:"worktree,omitempty"`
	Agent          string `json:"agent,omitempty"`
	PermissionMode string `json:"permissionMode,omitempty"`
	Message        string `json:"message"`
	Cwd            string `json:"cwd,omitempty"`
	Model          string `json:"model,omitempty"`
	ApprovalPolicy string `json:"approvalPolicy,omitempty"`
	Owner          string `json:"owner,omitempty"`
}

type SessionStarter interface {
	StartSession(ctx context.Context, options SessionStartOptions) (*Session, *SendResult, error)
}

type OptionSender interface {
	SendWithOptions(ctx context.Context, sess *Session, message string, options SendOptions) (*SendResult, error)
}

type SessionAccessChecker interface {
	EnsureWritable(ctx context.Context, sess *Session) error
}

func EnsureWritableSession(ctx context.Context, adapter Surface, sess *Session) error {
	if checker, ok := adapter.(SessionAccessChecker); ok {
		if err := checker.EnsureWritable(ctx, sess); err != nil {
			return err
		}
	}
	if IsReadOnlySession(sess) {
		return errors.New(ReadOnlySessionReason(sess))
	}
	return nil
}

type ModelOption struct {
	ID                        string   `json:"id"`
	DisplayName               string   `json:"displayName"`
	Description               string   `json:"description,omitempty"`
	Default                   bool     `json:"default,omitempty"`
	AllowsCustom              bool     `json:"allowsCustom,omitempty"`
	SupportedReasoningEfforts []string `json:"supportedReasoningEfforts,omitempty"`
	DefaultReasoningEffort    string   `json:"defaultReasoningEffort,omitempty"`
	ServiceTiers              []string `json:"serviceTiers,omitempty"`
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
	Source   string `json:"source,omitempty"`
}

type Exchange struct {
	User      string    `json:"user"`
	Assistant string    `json:"assistant"`
	Timestamp time.Time `json:"timestamp"`
	Source    string    `json:"source,omitempty"`
}

type GoalState struct {
	Objective string `json:"objective"`
	Status    string `json:"status"` // "active", "complete", ""
}

type StreamEvent struct {
	Kind    string        `json:"kind"`
	ID      string        `json:"id,omitempty"`
	Name    string        `json:"name,omitempty"`
	Text    string        `json:"text,omitempty"`
	Input   any           `json:"input,omitempty"`
	Output  any           `json:"output,omitempty"`
	Error   string        `json:"error,omitempty"`
	Context *ContextUsage `json:"context,omitempty"`
	Data    any           `json:"data,omitempty"`
}

type ContextUsage struct {
	UsedTokens            int64     `json:"usedTokens"`
	ContextWindow         int64     `json:"contextWindow"`
	CumulativeTokens      int64     `json:"cumulativeTokens,omitempty"`
	InputTokens           int64     `json:"inputTokens,omitempty"`
	CachedInputTokens     int64     `json:"cachedInputTokens,omitempty"`
	OutputTokens          int64     `json:"outputTokens,omitempty"`
	ReasoningOutputTokens int64     `json:"reasoningOutputTokens,omitempty"`
	Compacting            bool      `json:"compacting"`
	CompactionCount       int       `json:"compactionCount"`
	LastCompactedAt       time.Time `json:"lastCompactedAt,omitempty"`
	PreCompactTokens      int64     `json:"preCompactTokens,omitempty"`
	PostCompactTokens     int64     `json:"postCompactTokens,omitempty"`
	ReclaimedTokens       int64     `json:"reclaimedTokens,omitempty"`
	WindowEstimated       bool      `json:"windowEstimated,omitempty"`
	UpdatedAt             time.Time `json:"updatedAt,omitempty"`
}

type ContextUsageProvider interface {
	ContextUsage(ctx context.Context, sess *Session) (*ContextUsage, error)
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

type SessionCapabilities struct {
	Capabilities
	ReadOnly       bool   `json:"readOnly"`
	ReadOnlyReason string `json:"readOnlyReason,omitempty"`
}

func EffectiveCapabilities(session *Session, capabilities Capabilities) SessionCapabilities {
	if session != nil && session.Surface == KindClaude && session.Transport == "uds" {
		capabilities.Stream = false
		capabilities.Steer = false
		capabilities.Compact = false
		if !strings.HasPrefix(session.ID, "session_") && !strings.HasPrefix(session.ID, "cse_") {
			capabilities.Model = false
			capabilities.Interrupt = false
		}
	}
	if IsReadOnlySession(session) {
		return SessionCapabilities{ReadOnly: true, ReadOnlyReason: ReadOnlySessionReason(session)}
	}
	return SessionCapabilities{Capabilities: capabilities}
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

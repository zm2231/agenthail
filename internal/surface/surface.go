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
	ID                    string          `json:"id"`
	Surface               SurfaceKind     `json:"surface"`
	Name                  string          `json:"name"`
	Cwd                   string          `json:"cwd"`
	PID                   int             `json:"pid"`
	Status                SessionStatus   `json:"status"`
	Transcript            string          `json:"transcript"`
	HasLocal              bool            `json:"hasLocal"`
	Source                string          `json:"source,omitempty"`
	Transport             string          `json:"transport,omitempty"`
	ConfiguredModel       string          `json:"configuredModel,omitempty"`
	LastActive            time.Time       `json:"lastActive"`
	Runtime               *Runtime        `json:"runtime,omitempty"`
	Subagent              *Subagent       `json:"subagent,omitempty"`
	Subagents             *SubagentRollup `json:"subagents,omitempty"`
	StreamCursor          uint64          `json:"-"`
	StreamCursorSet       bool            `json:"-"`
	TranscriptOffset      int64           `json:"-"`
	TranscriptOffsetSet   bool            `json:"-"`
	TranscriptIdentity    string          `json:"-"`
	CodexPendingEventUser bool            `json:"-"`
	CodexPendingEventTurn string          `json:"-"`
	CodexCurrentTurnID    string          `json:"-"`
}

// Subagent identifies a session spawned by another session. RootID is the
// family's top-level session: the single destination for messages sent to the
// family. Depth counts spawn hops from the root, so a direct child is 1.
type Subagent struct {
	ParentID string `json:"parentId"`
	RootID   string `json:"rootId"`
	Depth    int    `json:"depth"`
	Nickname string `json:"nickname,omitempty"`
	Role     string `json:"role,omitempty"`
}

// SubagentRollup summarizes subagents a provider observes for a session
// without exposing them as sessions of their own (Claude Code subagents).
type SubagentRollup struct {
	Count   int `json:"count"`
	Working int `json:"working"`
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

type StreamCursorReader interface {
	StreamCursor(context.Context, *Session) (uint64, error)
}

type LocalTranscriptProvider interface {
	RequiresLocalTranscript(*Session) bool
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

type DeliveryEvidence string

const (
	EvidenceSubmitted         DeliveryEvidence = "submitted"
	EvidenceQueued            DeliveryEvidence = "queued"
	EvidenceTransportAccepted DeliveryEvidence = "transport_accepted"
	EvidenceHeld              DeliveryEvidence = "held"
	EvidenceDelivered         DeliveryEvidence = "delivered"
	EvidenceReplyObserved     DeliveryEvidence = "reply_observed"
	EvidenceFailed            DeliveryEvidence = "failed"
	EvidenceUnknown           DeliveryEvidence = "unknown"
	EvidenceExpired           DeliveryEvidence = "expired"
	EvidenceCanceled          DeliveryEvidence = "canceled"
)

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
	BusyDelivery    string `json:"busyDelivery,omitempty"`
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
	if err := ValidateRuntimeTransport(sess); err != nil {
		return err
	}
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

type RuntimeProblem string

const (
	RuntimeBridgeUnavailable RuntimeProblem = "bridge-unavailable"
	RuntimeStandaloneMissing RuntimeProblem = "standalone-missing"
	RuntimeStopped           RuntimeProblem = "runtime-stopped"
	RuntimeUnsupervised      RuntimeProblem = "unsupervised"
)

type RuntimeStatus struct {
	Name        string         `json:"name"`
	Reachable   bool           `json:"reachable"`
	Durable     bool           `json:"durable"`
	Backend     string         `json:"backend,omitempty"`
	Problem     RuntimeProblem `json:"problem,omitempty"`
	Detail      string         `json:"detail,omitempty"`
	Remediation string         `json:"remediation,omitempty"`
	Notes       []RuntimeNote  `json:"notes,omitempty"`
}

type RuntimeNote struct {
	Problem     RuntimeProblem `json:"problem"`
	Message     string         `json:"message"`
	Remediation string         `json:"remediation,omitempty"`
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
	InputTurnID     string        `json:"inputTurnId,omitempty"`
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

type StreamEvent struct {
	Role             string        `json:"role,omitempty"`
	Origin           string        `json:"origin,omitempty"`
	Sender           string        `json:"sender,omitempty"`
	Title            string        `json:"title,omitempty"`
	Status           string        `json:"status,omitempty"`
	CallID           string        `json:"callId,omitempty"`
	Attachment       *Attachment   `json:"attachment,omitempty"`
	Truncated        bool          `json:"truncated,omitempty"`
	TruncationReason string        `json:"truncationReason,omitempty"`
	ID               string        `json:"id,omitempty"`
	ProviderKey      string        `json:"providerKey,omitempty"`
	Cursor           uint64        `json:"cursor,omitempty"`
	Version          uint64        `json:"version,omitempty"`
	Operation        string        `json:"operation,omitempty"`
	Final            bool          `json:"final,omitempty"`
	TurnID           string        `json:"turnId,omitempty"`
	Timestamp        time.Time     `json:"timestamp,omitempty"`
	Kind             string        `json:"kind"`
	Text             string        `json:"text"`
	Context          *ContextUsage `json:"context,omitempty"`
	Goal             *GoalState    `json:"goal,omitempty"`
}

type StreamEventClass string

const (
	StreamEventMessage    StreamEventClass = "message"
	StreamEventToolCall   StreamEventClass = "tool_call"
	StreamEventToolResult StreamEventClass = "tool_result"
	StreamEventTerminal   StreamEventClass = "terminal"
	StreamEventOther      StreamEventClass = "other"
)

func (e StreamEvent) Class() StreamEventClass {
	switch e.Kind {
	case "text", "message", "assistant":
		return StreamEventMessage
	case "tool_use", "toolCall", "tool_call":
		return StreamEventToolCall
	case "tool_result", "toolResult":
		return StreamEventToolResult
	case "done", "source-error":
		return StreamEventTerminal
	default:
		return StreamEventOther
	}
}

func (e StreamEvent) Failed() bool {
	if e.Class() != StreamEventTerminal {
		return false
	}
	switch e.Status {
	case "failed", "error", "cancelled", "canceled":
		return true
	default:
		return e.Kind == "source-error"
	}
}

func StreamEventDelta(event StreamEvent, previous string) string {
	if event.Operation == "append" {
		return event.Text
	}
	if strings.HasPrefix(event.Text, previous) {
		return strings.TrimPrefix(event.Text, previous)
	}
	return event.Text
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
	ContextWindowSource   string    `json:"contextWindowSource,omitempty"`
	UpdatedAt             time.Time `json:"updatedAt,omitempty"`
}

type ContextUsageProvider interface {
	ContextUsage(ctx context.Context, sess *Session) (*ContextUsage, error)
}

// GoalUpdate is the typed control payload supported by Codex thread/goal/set.
// A nil field is omitted, except ClearTokenBudget, which explicitly sends a
// JSON null token budget.
type GoalUpdate struct {
	Objective        *string `json:"objective,omitempty"`
	Status           *string `json:"status,omitempty"`
	TokenBudget      *int64  `json:"tokenBudget,omitempty"`
	ClearTokenBudget bool    `json:"-"`
}

type GoalController interface {
	UpdateGoal(context.Context, *Session, GoalUpdate) error
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
		capabilities.Stream = capabilities.Stream && session.HasLocal && session.Transcript != ""
		if !strings.HasPrefix(session.ID, "session_") && !strings.HasPrefix(session.ID, "cse_") {
			capabilities.Steer = false
			capabilities.Compact = false
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

// TurnInterrupter accepts interruption only when the adapter can verify the
// exact active turn selected by the caller.
type TurnInterrupter interface {
	InterruptTurn(context.Context, *Session, string) error
}

// LocalStatusSource lets the catalog follow a listed session's status between
// discovery passes from local files alone, without provider calls.
// LocalStatusFiles names the files whose change can change the status;
// LocalStatus re-derives Status and LastActive from them by the same rule List
// applies, so a discovery pass never reverts a status the files still support.
type LocalStatusSource interface {
	LocalStatusFiles(session Session) []string
	LocalStatus(ctx context.Context, session Session) (Session, error)
}

// CatalogListCompleteness declares whether a successful List result is a
// complete enumeration suitable for omission reconciliation.
type CatalogListCompleteness interface {
	CatalogListComplete() bool
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

var ErrStreamWindow = errors.New("stream window elapsed")

type errUnsupported struct{}

func (errUnsupported) Error() string { return "operation not supported by this surface" }
func (errUnsupported) Is(target error) bool {
	_, ok := target.(errUnsupported)
	return ok
}

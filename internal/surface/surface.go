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

// WaitingKind names what an open turn is waiting for from its user.
type WaitingKind string

const (
	WaitingQuestion WaitingKind = "question"
	WaitingApproval WaitingKind = "approval"
	WaitingInput    WaitingKind = "input"
	// WaitingDialog is a dialog open in the session's own interface, such as
	// a settings panel, which only that interface can settle.
	WaitingDialog WaitingKind = "dialog"
)

// Waiting marks a busy turn that cannot continue until its user answers. ID
// is stable for one question, so a new ID means a new question.
type Waiting struct {
	Kind  WaitingKind `json:"kind"`
	ID    string      `json:"id"`
	Since time.Time   `json:"since,omitzero"`
}

// BackgroundTasks is work a session started that runs on after its turn ends:
// shells, subagents and monitors. Running counts only tasks that are activity;
// an ambient task, such as a monitor waiting for events, is listed but does not
// keep the session's work from being done. Since is when the oldest running
// task started. Source is where the tasks were read: the Claude host, the
// transcript, or only the process's own status, which names no tasks.
type BackgroundTasks struct {
	Running bool             `json:"running"`
	Tasks   []BackgroundTask `json:"tasks,omitempty"`
	Since   time.Time        `json:"since,omitzero"`
	Source  string           `json:"source"`
}

const (
	BackgroundSourceHost       = "host"
	BackgroundSourceTranscript = "transcript"
	BackgroundSourceStatus     = "status"
)

// BackgroundTask kinds.
const (
	BackgroundShell   = "shell"
	BackgroundAgent   = "agent"
	BackgroundMonitor = "monitor"
)

type BackgroundTask struct {
	ID          string    `json:"id"`
	Kind        string    `json:"kind"`
	Description string    `json:"description,omitempty"`
	ParentID    string    `json:"parentId,omitempty"`
	Ambient     bool      `json:"ambient,omitempty"`
	Since       time.Time `json:"since,omitzero"`
}

// BackgroundRunning reports whether background tasks keep a session's work
// from being done.
func BackgroundRunning(tasks *BackgroundTasks) bool {
	return tasks != nil && tasks.Running
}

// SameBackground reports whether two readings list the same background work.
func SameBackground(left, right *BackgroundTasks) bool {
	if left == nil || right == nil {
		return left == right
	}
	if left.Running != right.Running || left.Source != right.Source || !left.Since.Equal(right.Since) || len(left.Tasks) != len(right.Tasks) {
		return false
	}
	for index, task := range left.Tasks {
		other := right.Tasks[index]
		if task.ID != other.ID || task.Kind != other.Kind || task.Description != other.Description || task.ParentID != other.ParentID || task.Ambient != other.Ambient || !task.Since.Equal(other.Since) {
			return false
		}
	}
	return true
}

// SameWaiting reports whether two waiting states describe the same question.
func SameWaiting(left, right *Waiting) bool {
	if left == nil || right == nil {
		return left == right
	}
	return left.Kind == right.Kind && left.ID == right.ID
}

// Session is one conversation. ID is the provider's native conversation id
// (Claude session id, Codex thread id) and never changes. RuntimeKind is what
// carries it now and picks its adapter. PID, Transport and Bridge (the Claude
// Remote Control id) describe the process currently attached to the
// conversation and can come and go. ForkedFrom is the conversation this one was
// branched from.
type Session struct {
	ID                    string            `json:"id"`
	Surface               SurfaceKind       `json:"surface"`
	Name                  string            `json:"name"`
	Cwd                   string            `json:"cwd"`
	PID                   int               `json:"pid"`
	Status                SessionStatus     `json:"status"`
	WaitingOn             *Waiting          `json:"waitingOn,omitempty"`
	BackgroundTasks       *BackgroundTasks  `json:"backgroundTasks,omitempty"`
	Transcript            string            `json:"transcript"`
	HasLocal              bool              `json:"hasLocal"`
	Source                string            `json:"source,omitempty"`
	Transport             string            `json:"transport,omitempty"`
	RuntimeKind           RuntimeKind       `json:"runtimeKind,omitempty"`
	ConfiguredModel       string            `json:"configuredModel,omitempty"`
	Bridge                string            `json:"bridge,omitempty"`
	ForkedFrom            string            `json:"forkedFrom,omitempty"`
	LastActive            time.Time         `json:"lastActive"`
	StartedAt             time.Time         `json:"startedAt,omitzero"`
	Runtime               *Runtime          `json:"runtime,omitempty"`
	Subagent              *Subagent         `json:"subagent,omitempty"`
	Subagents             *SubagentRollup   `json:"subagents,omitempty"`
	StreamCursor          uint64            `json:"-"`
	StreamCursorSet       bool              `json:"-"`
	TranscriptOffset      int64             `json:"-"`
	TranscriptOffsetSet   bool              `json:"-"`
	TranscriptIdentity    string            `json:"-"`
	CodexPendingEventUser bool              `json:"-"`
	CodexPendingEventTurn string            `json:"-"`
	CodexCurrentTurnID    string            `json:"-"`
	ClaudeClosingTurn     ClaudeClosingTurn `json:"-"`
}

// ClaudeClosingTurn is a Claude turn whose end_turn message has been read but
// may still have content blocks to write. A stream that resumes at
// TranscriptOffset finishes it at the first record outside MessageID, or once
// its process records idle no earlier than At, provided the transcript bytes
// before that offset still match Anchor.
type ClaudeClosingTurn struct {
	TurnID    string
	MessageID string
	Key       string
	Anchor    string
	At        time.Time
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

// Delegated reports whether an agent, not the user, started the session: a
// spawned subagent, a `codex exec` run, or one of Codex's own review and
// compaction threads.
func (s Session) Delegated() bool {
	return s.Subagent != nil || s.Source == codexSourceExec || s.Source == codexSourceSubagent
}

// Session.Source holds a Codex source string, or the key of an object source.
const (
	codexSourceExec     = "exec"
	codexSourceSubagent = "subAgent"
)

// ConversationID is the root of a subagent's family, or the session itself.
func (s Session) ConversationID() string {
	if s.Subagent == nil {
		return s.ID
	}
	if s.Subagent.RootID != "" {
		return s.Subagent.RootID
	}
	if s.Subagent.ParentID != "" {
		return s.Subagent.ParentID
	}
	return s.ID
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

// TranscriptItemReader is implemented by an adapter that can derive again,
// from its transcript, every timeline item recorded at or after an offset.
type TranscriptItemReader interface {
	TranscriptItems(ctx context.Context, session *Session, from int64) ([]TimelineItem, error)
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
	return "Codex session is read only: Codex Desktop created it, or Codex recorded no creator, and the Desktop bridge is unavailable; run 'agenthail launch codex'"
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

// ErrDeliveryHeld marks an unavailable delivery whose target is running but
// cannot accept a message yet; the message waits in the queue until it can.
var ErrDeliveryHeld = errors.New("target is running but not reachable yet")

// ErrNothingToSteer is a steer that found the session idle: its turn ended
// before the steer reached it.
var ErrNothingToSteer = errors.New("session idle; nothing to steer")

func IsDeliveryHeld(err error) bool {
	return IsDeliveryUnavailable(err) && errors.Is(err, ErrDeliveryHeld)
}

// DeliveryRefused marks an unavailable delivery the target refused to start,
// such as a resume Claude declines. The message waits in the queue, but only a
// person can clear the reason, so the refusal is announced.
func DeliveryRefused(err error) error {
	if err == nil {
		return nil
	}
	return DeliveryUnavailable(refusedError{err: err})
}

func IsDeliveryRefused(err error) bool {
	var target refusedError
	return IsDeliveryUnavailable(err) && errors.As(err, &target)
}

type refusedError struct{ err error }

func (e refusedError) Error() string { return e.err.Error() }
func (e refusedError) Unwrap() error { return e.err }

type DeliveryTerminalKind string

const (
	DeliveryTargetMissing        DeliveryTerminalKind = "target_missing"
	DeliveryAuthenticationNeeded DeliveryTerminalKind = "authentication_needed"
	DeliveryAccessDenied         DeliveryTerminalKind = "access_denied"
	DeliveryInvalidRequest       DeliveryTerminalKind = "invalid_request"
	DeliveryOwnershipConflict    DeliveryTerminalKind = "ownership_conflict"
	DeliverySigninOff            DeliveryTerminalKind = "signin_off"
	DeliverySigninUnreadable     DeliveryTerminalKind = "signin_unreadable"
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
	case DeliverySigninOff:
		return "browser sign-in turned off"
	case DeliverySigninUnreadable:
		return "browser sign-in unreadable"
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
	Author          Author `json:"-"`
	// Uploads are the person's attached files. Route.Send turns them into
	// Images for a route that takes image input and into stored paths in the
	// message text for everything else.
	Uploads Uploads `json:"-"`
	Images  Uploads `json:"-"`
}

// HoldsForQueue reports whether the send carries parts a busy-session steer
// cannot deliver, so it must wait in the queue for a turn of its own.
func (o SendOptions) HoldsForQueue() bool {
	return o.Model != "" || !o.TurnOptions.Empty() || len(o.Uploads) > 0
}

// Author records who wrote a message. A composer declares AuthorPerson when a
// person typed it; any send that does not declare it is unattributed.
type Author string

const AuthorPerson Author = "person"

// HumanTurnSender delivers a person's message as that session's own user
// turn, separate from the peer transport agents use to message each other.
type HumanTurnSender interface {
	SendHumanTurn(ctx context.Context, sess *Session, message string, images Uploads) (*SendResult, error)
}

// ImageRouter is implemented by an adapter whose runtime takes images as
// model input on some routes. Every other route receives each upload as its
// stored path in the message text.
type ImageRouter interface {
	NativeImages(session *Session, route Route) bool
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
	ConfigRoot     string `json:"configRoot,omitempty"`
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

// Waker is a surface whose sends first wake a session that cannot take a
// message as it is, such as a closed Claude conversation they resume.
type Waker interface {
	NeedsWake(ctx context.Context, sess *Session) bool
}

// ConversationKeeper is a surface that can tell a conversation it would
// resume from one whose history is gone.
type ConversationKeeper interface {
	ConversationExists(ctx context.Context, sess *Session) (bool, error)
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

type RuntimeProblem string

const (
	RuntimeBridgeUnavailable RuntimeProblem = "bridge-unavailable"
	RuntimeCodexMissing      RuntimeProblem = "codex-missing"
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
	Status          SessionStatus    `json:"status"`
	ActiveTurnID    string           `json:"activeTurnId,omitempty"`
	TerminalTurnID  string           `json:"terminalTurnId,omitempty"`
	CompletedTurnID string           `json:"completedTurnId,omitempty"`
	InputTurnID     string           `json:"inputTurnId,omitempty"`
	WaitingOn       *Waiting         `json:"waitingOn,omitempty"`
	BackgroundTasks *BackgroundTasks `json:"backgroundTasks,omitempty"`
	Reply           *ReplyResult     `json:"reply,omitempty"`
}

type ReplyResult struct {
	Text     string `json:"text"`
	UserText string `json:"userText"` // last user message (for context)
	Done     bool   `json:"done"`
	Error    string `json:"error"`
	Source   string `json:"source,omitempty"`
}

// Exchange is a prompt and the reply that ended its turn. Origin names who
// sent a prompt that was not typed by the person: "peer", with Sender naming
// the agent, or a prompt the provider injected, such as "task-notification".
type Exchange struct {
	User      string        `json:"user"`
	Origin    string        `json:"origin,omitempty"`
	Sender    string        `json:"sender,omitempty"`
	Assistant string        `json:"assistant"`
	Timestamp time.Time     `json:"timestamp"`
	Source    string        `json:"source,omitempty"`
	Turn      *TurnMetadata `json:"turn,omitempty"`
}

type StreamEvent struct {
	Role             string         `json:"role,omitempty"`
	Origin           string         `json:"origin,omitempty"`
	Sender           string         `json:"sender,omitempty"`
	Title            string         `json:"title,omitempty"`
	Status           string         `json:"status,omitempty"`
	ResetsAt         string         `json:"resetsAt,omitempty"`
	CallID           string         `json:"callId,omitempty"`
	Attachment       *Attachment    `json:"attachment,omitempty"`
	Files            []AttachedFile `json:"files,omitempty"`
	Truncated        bool           `json:"truncated,omitempty"`
	TruncationReason string         `json:"truncationReason,omitempty"`
	ID               string         `json:"id,omitempty"`
	ProviderKey      string         `json:"providerKey,omitempty"`
	Cursor           uint64         `json:"cursor,omitempty"`
	Version          uint64         `json:"version,omitempty"`
	Operation        string         `json:"operation,omitempty"`
	Final            bool           `json:"final,omitempty"`
	TurnID           string         `json:"turnId,omitempty"`
	Timestamp        time.Time      `json:"timestamp,omitempty"`
	Kind             string         `json:"kind"`
	Text             string         `json:"text"`
	Context          *ContextUsage  `json:"context,omitempty"`
	Goal             *GoalState     `json:"goal,omitempty"`
	Turn             *TurnMetadata  `json:"turn,omitempty"`
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

// FailureReason is why a failed terminal event failed: the text a failed
// turn's end carries, such as an API refusal, else its status.
func (e StreamEvent) FailureReason() string {
	for _, reason := range []string{e.Text, e.Status} {
		if reason != "" {
			return reason
		}
	}
	return "terminal failure"
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

// GoalCommandRunner is a surface whose session sets and clears its goal by
// running a command as its own turn, so a busy session takes it once idle.
type GoalCommandRunner interface {
	// GoalCommand is the command that sets objective on the session, or clears
	// its goal when objective is empty, or why the session would refuse it.
	GoalCommand(sess *Session, objective string) (string, error)
	// RunGoalCommand runs command on an idle session and returns once the
	// session reports its outcome.
	RunGoalCommand(ctx context.Context, sess *Session, command string) error
}

type Capabilities struct {
	Send   bool `json:"send"`
	Stream bool `json:"stream"`
	Reply  bool `json:"reply"`
	// Goal is a session whose goal is paused, resumed and budgeted here, and
	// whose goal events relay.
	Goal bool `json:"goal"`
	// GoalSet is a session whose goal is set, replaced and cleared here.
	GoalSet bool `json:"goalSet"`
	// GoalStatus is a session whose goal status GoalGet reads.
	GoalStatus bool `json:"goalStatus"`
	Compact    bool `json:"compact"`
	Model      bool `json:"model"`
	Interrupt  bool `json:"interrupt"`
	Steer      bool `json:"steer"`
	Attach     bool `json:"attach"`
	// Respond and PermissionMode are a session whose open requests a person
	// answers here and whose permission mode changes while it runs.
	Respond        bool `json:"respond"`
	PermissionMode bool `json:"permissionMode"`
	// Commands is a session that takes Claude Remote Control's context, usage,
	// status, effort and rename commands.
	Commands bool `json:"commands"`
	// Lifecycle is what the session's runtime does to the session itself.
	Lifecycle Lifecycle `json:"lifecycle"`
}

// Lifecycle names the operations on a session's process that its runtime
// supports: report its state, read its log, stop it, and start it again.
type Lifecycle struct {
	Status bool `json:"status"`
	Logs   bool `json:"logs"`
	Stop   bool `json:"stop"`
	Resume bool `json:"resume"`
}

type SessionCapabilities struct {
	Capabilities
	ReadOnly       bool   `json:"readOnly"`
	ReadOnlyReason string `json:"readOnlyReason,omitempty"`
}

// SessionCapabilitySource is implemented by an adapter whose capabilities
// differ by session. Every other adapter's sessions have its Capabilities.
type SessionCapabilitySource interface {
	SessionCapabilities(session *Session) Capabilities
}

// CapabilitiesFor is what the adapter can do for the session.
func CapabilitiesFor(adapter Surface, session *Session) Capabilities {
	if source, ok := adapter.(SessionCapabilitySource); ok {
		return source.SessionCapabilities(session)
	}
	return adapter.Capabilities()
}

// EffectiveCapabilities is what may be done to the session: nothing when it is
// read-only, otherwise what its adapter can do for it.
func EffectiveCapabilities(adapter Surface, session *Session) SessionCapabilities {
	if IsReadOnlySession(session) {
		return SessionCapabilities{ReadOnly: true, ReadOnlyReason: ReadOnlySessionReason(session)}
	}
	return SessionCapabilities{Capabilities: CapabilitiesFor(adapter, session)}
}

type Surface interface {
	Name() SurfaceKind
	// RuntimeKinds are the runtimes whose sessions this adapter serves.
	RuntimeKinds() []RuntimeKind
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

// CatalogLister lists sessions for the daemon catalog and reports whether the
// result enumerates every session the runtime holds. Only a complete result
// retires the sessions it omits. Adapters without it list completely.
type CatalogLister interface {
	CatalogList(ctx context.Context) ([]Session, bool, error)
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

var ErrSessionNotFound = errors.New("no session matched")

type errUnsupported struct{}

func (errUnsupported) Error() string { return "operation not supported by this surface" }
func (errUnsupported) Is(target error) bool {
	_, ok := target.(errUnsupported)
	return ok
}

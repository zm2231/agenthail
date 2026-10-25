package voice

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type Cursor struct {
	Source   string `json:"source"`
	Position int64  `json:"position"`
}

type Event struct {
	Sequence int64          `json:"sequence"`
	Method   string         `json:"method"`
	Params   map[string]any `json:"params"`
}

// RejectedError is a call start Codex definitely refused, so the call ended.
type RejectedError struct{ Reason string }

func (e *RejectedError) Error() string { return e.Reason }

type Batch struct {
	Cursor Cursor
	Events []Event
	Lost   bool
}

// Deliverer sends a delegated turn to its target session and updates session
// when another conversation took it. delivery.Dispatcher is one.
type Deliverer interface {
	DeliverWithoutQueue(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions) (*delivery.Receipt, error)
}

type Provider interface {
	Create(context.Context, string, string) (*surface.Session, error)
	Cursor(context.Context, *surface.Session) (Cursor, error)
	// Poll returns the call's events after the cursor as soon as there are
	// any, and an empty batch once wait has passed without one.
	Poll(ctx context.Context, after Cursor, threadID string, wait time.Duration) (Batch, error)
	Request(context.Context, *surface.Session, string, map[string]any) error
	Interrupt(context.Context, *surface.Session) error
	// Release frees what Cursor opened for one call on the thread, the call
	// whose cursor it was, once nothing will poll it again.
	Release(threadID string, cursor Cursor)
}

// DynamicToolResponder completes a caller-owned Agenthail tool invoked by the
// persistent Codex operator. It is intentionally separate from Provider so
// other voice transports remain usable without exposing a tool callback.
type DynamicToolResponder interface {
	RespondDynamicToolCall(ctx context.Context, requestID, threadID string, success bool, text string) error
}

// Target is the selected coding session behind a voice call. The audio plane
// remains the persistent Codex voice operator; targets never own credentials or
// a second realtime connection.
type Target struct {
	Session *surface.Session
	Adapter surface.Surface
}

type TargetResolver func(context.Context, string) (*Target, error)

type SessionStreamProvider interface {
	PrepareSessionStream(context.Context, *surface.Session) (sessionstream.Subscription, error)
}

type State struct {
	Protocol       int              `json:"protocol"`
	Session        *surface.Session `json:"session,omitempty"`
	Phase          string           `json:"phase"`
	AttemptID      string           `json:"attemptId,omitempty"`
	SkillDigest    string           `json:"skillDigest,omitempty"`
	ToolsDigest    string           `json:"toolsDigest,omitempty"`
	Message        string           `json:"message,omitempty"`
	SDP            string           `json:"sdp,omitempty"`
	Events         []Event          `json:"events"`
	Truncated      bool             `json:"truncated"`
	Occupied       bool             `json:"occupied"`
	TextReceipt    string           `json:"textReceipt,omitempty"`
	Target         *surface.Session `json:"target,omitempty"`
	AudioProvider  string           `json:"audioProvider,omitempty"`
	CodingProvider string           `json:"codingProvider,omitempty"`
	TerminalReason string           `json:"terminalReason,omitempty"`
	// InputMuted is the phone's last reported microphone state for the call:
	// muted, or held by the system. InputRevision orders those reports.
	InputMuted    bool  `json:"inputMuted"`
	InputRevision int64 `json:"inputRevision,omitempty"`
	// Brief is what the call was told about its selected session.
	Brief *BriefInfo `json:"brief,omitempty"`
}

type Action struct {
	Action    string `json:"action"`
	AttemptID string `json:"attemptId"`
	SDP       string `json:"sdp"`
	Text      string `json:"text"`
	MessageID string `json:"messageId"`
	TargetID  string `json:"targetId"`
	// Muted and InputRevision carry an input-state report.
	Muted         *bool `json:"muted,omitempty"`
	InputRevision int64 `json:"inputRevision,omitempty"`
}

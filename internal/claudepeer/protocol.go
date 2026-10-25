package claudepeer

import (
	"encoding/json"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	DefaultSocketDir    = "/tmp/cc-socks"
	PeerProtocolVersion = 1
	defaultSocketDir    = DefaultSocketDir
	peerProtocol        = PeerProtocolVersion
	maxLineBytes        = 64 << 10
	maxFrames           = 16
	readDeadline        = 5 * time.Second
	firstLineDeadline   = 3 * time.Second
	noticeDeadline      = time.Second
)

type frame struct {
	MsgV  int    `json:"msgV"`
	MsgID string `json:"msg_id"`
	// UUID is the id Claude gives the message and names in its hold reports.
	UUID     string          `json:"uuid,omitempty"`
	Type     string          `json:"type"`
	Message  json.RawMessage `json:"message"`
	Priority string          `json:"priority,omitempty"`
	From     string          `json:"from"`
}

type messageBody struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type controlRequest struct {
	TargetSocket string `json:"targetSocket"`
	SenderID     string `json:"senderId"`
	Message      string `json:"message"`
	ReplySocket  string `json:"replySocket,omitempty"`
	MsgID        string `json:"msgId,omitempty"`
	PersonName   string `json:"personName,omitempty"`
}

// Response is a peer delivery answer, from a worker's control socket or the
// daemon's peer manager.
type Response struct {
	OK     bool                `json:"ok"`
	Result *surface.SendResult `json:"result,omitempty"`
	Error  *ResponseError      `json:"error,omitempty"`
}

type ResponseError struct {
	Class string `json:"class"`
	Kind  string `json:"kind,omitempty"`
	Error string `json:"error"`
}

func (r Response) Outcome(malformed string) (*surface.SendResult, error) {
	if r.OK && r.Result != nil {
		return r.Result, nil
	}
	if r.Error == nil {
		return nil, surface.DeliveryOutcomeUnknown(errors.New(malformed))
	}
	switch r.Error.Class {
	case "terminal":
		return nil, surface.DeliveryTerminal(errors.New(r.Error.Error), surface.DeliveryTerminalKind(r.Error.Kind))
	case "unavailable":
		return nil, surface.DeliveryUnavailable(errors.New(r.Error.Error))
	default:
		return nil, surface.DeliveryOutcomeUnknown(errors.New(r.Error.Error))
	}
}

func classifyControlError(err error) *ResponseError {
	var terminal surface.DeliveryTerminalError
	if errors.As(err, &terminal) {
		return &ResponseError{Class: "terminal", Kind: string(terminal.Kind), Error: terminal.Error()}
	}
	var unavailable surface.DeliveryUnavailableError
	if errors.As(err, &unavailable) {
		return &ResponseError{Class: "unavailable", Error: unavailable.Error()}
	}
	return &ResponseError{Class: "unknown", Error: err.Error()}
}

func mustJSON(v any) json.RawMessage { b, _ := json.Marshal(v); return b }

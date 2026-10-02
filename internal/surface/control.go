package surface

import "context"

type ControlRequest struct {
	Action string `json:"action"`
	Value  string `json:"value,omitempty"`
}

type ControlResult struct {
	SessionID string           `json:"sessionId"`
	RequestID string           `json:"requestId,omitempty"`
	TurnID    string           `json:"turnId,omitempty"`
	Action    string           `json:"action"`
	Evidence  DeliveryEvidence `json:"evidence"`
	Source    string           `json:"source"`
	Output    string           `json:"output,omitempty"`
	Detail    string           `json:"detail,omitempty"`
}

type Controller interface {
	Control(context.Context, *Session, ControlRequest) (*ControlResult, error)
}

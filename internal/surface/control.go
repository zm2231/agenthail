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

// Controller runs a control and reports the evidence it observed. A result
// can accompany an error: an accepted request whose effect was not observed
// returns its request id with unknown evidence.
type Controller interface {
	Control(context.Context, *Session, ControlRequest) (*ControlResult, error)
}

// SteerWithEvidence treats a surface without request-linked controls as
// confirming the steer when Steer returns.
func SteerWithEvidence(ctx context.Context, adapter Surface, session *Session, message string) (*ControlResult, error) {
	if controller, ok := adapter.(Controller); ok {
		return controller.Control(ctx, session, ControlRequest{Action: "steer", Value: message})
	}
	if err := adapter.Steer(ctx, session, message); err != nil {
		return nil, err
	}
	return &ControlResult{SessionID: session.ID, Action: "steer", Evidence: EvidenceDelivered, Source: string(adapter.Name())}, nil
}

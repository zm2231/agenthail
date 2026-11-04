package surface

import "context"

type ControlRequest struct {
	Action string  `json:"action"`
	Value  string  `json:"value,omitempty"`
	Images Uploads `json:"-"`
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
	Change    *ModelChange     `json:"change,omitempty"`
}

// Controller runs a control and reports the evidence it observed. A result
// can accompany an error: an accepted request whose effect was not observed
// returns its request id with unknown evidence.
type Controller interface {
	Control(context.Context, *Session, ControlRequest) (*ControlResult, error)
}

// ImageSteerer steers a running turn with images, for a surface whose steer is
// not a Control.
type ImageSteerer interface {
	SteerWithImages(ctx context.Context, session *Session, message string, images Uploads) error
}

// SteerParts is what a steer into session delivers for this send. It is false
// when the send can't join a running turn: per-turn options, or an image on a
// session whose steer takes none. Other files are named in the text.
func SteerParts(adapter Surface, session *Session, message string, options SendOptions) (string, Uploads, bool) {
	if options.HoldsForQueue() {
		return "", nil, false
	}
	takesImages := EffectiveCapabilities(adapter, session).SteerImages
	if !takesImages && len(options.Uploads.Images()) > 0 {
		return "", nil, false
	}
	text, images := options.Uploads.Compose(message, takesImages)
	return text, images, true
}

// SteerWithEvidence treats a surface without request-linked controls as
// confirming the steer when Steer returns.
func SteerWithEvidence(ctx context.Context, adapter Surface, session *Session, message string, images Uploads) (*ControlResult, error) {
	if _, routed := adapter.(Router); !routed {
		message = LabelCLI(ctx, message)
	}
	if controller, ok := adapter.(Controller); ok {
		return controller.Control(ctx, session, ControlRequest{Action: "steer", Value: message, Images: images})
	}
	var err error
	if len(images) > 0 {
		steerer, ok := adapter.(ImageSteerer)
		if !ok {
			return nil, ErrUnsupported
		}
		err = steerer.SteerWithImages(ctx, session, message, images)
	} else {
		err = adapter.Steer(ctx, session, message)
	}
	if err != nil {
		return nil, err
	}
	return &ControlResult{SessionID: session.ID, Action: "steer", Evidence: EvidenceDelivered, Source: string(adapter.Name())}, nil
}

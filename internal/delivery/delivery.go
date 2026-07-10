package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var ErrTargetBusy = errors.New("target is active and queuing is disabled")

type Disposition string

const (
	DispositionAccepted Disposition = "accepted"
	DispositionQueued   Disposition = "queued"
)

type Receipt struct {
	Disposition Disposition `json:"disposition"`
	SessionID   string      `json:"sessionId"`
	TurnID      string      `json:"turnId,omitempty"`
	QueueID     int64       `json:"queueId,omitempty"`
	Reason      string      `json:"reason,omitempty"`
}

type Dispatcher struct {
	Registry *registry.Registry
}

func (d Dispatcher) Deliver(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string) (*Receipt, error) {
	return d.DeliverWithOptions(ctx, adapter, session, message, deliveryKey, surface.SendOptions{})
}

func (d Dispatcher) DeliverWithOptions(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions) (*Receipt, error) {
	return d.deliver(ctx, adapter, session, message, deliveryKey, options, true)
}

func (d Dispatcher) DeliverWithoutQueue(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions) (*Receipt, error) {
	return d.deliver(ctx, adapter, session, message, deliveryKey, options, false)
}

func (d Dispatcher) deliver(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*Receipt, error) {
	var result *surface.SendResult
	var err error
	if options.Model != "" {
		sender, ok := adapter.(surface.OptionSender)
		if !ok {
			return nil, fmt.Errorf("%s does not support per-message model selection", adapter.Name())
		}
		result, err = sender.SendWithOptions(ctx, session, message, options)
	} else {
		result, err = adapter.Send(ctx, session, message)
	}
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, fmt.Errorf("%s returned an empty delivery result", adapter.Name())
	}
	if result.Accepted {
		return &Receipt{Disposition: DispositionAccepted, SessionID: session.ID, TurnID: result.UUID}, nil
	}
	if !allowQueue {
		return nil, ErrTargetBusy
	}
	if d.Registry == nil {
		return nil, fmt.Errorf("%s is busy and no registry is available for queuing", adapter.Name())
	}
	queueID, err := d.Registry.QueueMessageWithOptions(session.ID, message, deliveryKey, options)
	if err != nil {
		return nil, err
	}
	return &Receipt{Disposition: DispositionQueued, SessionID: session.ID, TurnID: result.UUID, QueueID: queueID, Reason: "target_busy"}, nil
}

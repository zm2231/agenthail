package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var ErrTargetBusy = errors.New("target is active and queuing is disabled")

type Receipt struct {
	Evidence   surface.DeliveryEvidence `json:"evidence"`
	Status     string                   `json:"status"`
	SessionID  string                   `json:"sessionId"`
	TurnID     string                   `json:"turnId,omitempty"`
	QueueID    int64                    `json:"queueId,omitempty"`
	DeliveryID int64                    `json:"deliveryId,omitempty"`
	Detail     string                   `json:"detail,omitempty"`
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

func (d Dispatcher) Compact(ctx context.Context, adapter surface.Surface, session *surface.Session) (*Receipt, error) {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	if adapter.Name() == surface.KindClaude && d.Registry != nil {
		queueID, err := d.Registry.QueueCompact(session.ID)
		if err != nil {
			return nil, err
		}
		return &Receipt{Evidence: surface.EvidenceQueued, SessionID: session.ID, QueueID: queueID, Detail: "compact will run when the target is idle"}, nil
	}
	if err := adapter.Compact(ctx, session); err != nil {
		d.record(registry.HistoryEntry{Kind: failureKind(err, "control-failed"), SessionID: session.ID, Message: "compact", Error: err.Error()})
		return nil, err
	}
	d.record(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, Message: "compact", Evidence: surface.EvidenceDelivered})
	return &Receipt{Evidence: surface.EvidenceDelivered, SessionID: session.ID}, nil
}

func (d Dispatcher) Steer(ctx context.Context, adapter surface.Surface, session *surface.Session, message string) (*Receipt, error) {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	if err := adapter.Steer(ctx, session, message); err != nil {
		d.record(registry.HistoryEntry{Kind: failureKind(err, "control-failed"), SessionID: session.ID, Message: "steer", Error: err.Error()})
		return nil, err
	}
	d.record(registry.HistoryEntry{Kind: "control-accepted", SessionID: session.ID, Message: "steer"})
	return &Receipt{Evidence: surface.EvidenceDelivered, SessionID: session.ID}, nil
}

// failureKind keeps an uncertain outcome distinct from a confirmed failure so every surface shows the same evidence.
func failureKind(err error, fallback string) string {
	if surface.IsDeliveryOutcomeUnknown(err) {
		return "unknown"
	}
	return fallback
}

func (d Dispatcher) deliver(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*Receipt, error) {
	if err := options.TurnOptions.Validate(adapter.Name()); err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	ctx = surface.WithSourceSessionID(ctx, options.SourceSessionID)
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	baselineCompletionID := ""
	if d.Registry != nil {
		state, found, stateErr := d.Registry.RuntimeState(session.ID)
		if stateErr != nil {
			d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: message, Error: stateErr.Error()})
		} else if found {
			baselineCompletionID = state.CompletedTurnID
		} else if observation, observeErr := adapter.Observe(ctx, session); observeErr == nil && observation != nil {
			baselineCompletionID = observation.CompletedTurnID
		}
	}
	var result *surface.SendResult
	var err error
	if options.Model != "" || !options.TurnOptions.Empty() {
		sender, ok := adapter.(surface.OptionSender)
		if !ok {
			return nil, fmt.Errorf("%s does not support per-message model selection", adapter.Name())
		}
		result, err = sender.SendWithOptions(ctx, session, message, options)
	} else {
		result, err = adapter.Send(ctx, session, message)
	}
	if err != nil {
		if surface.IsDeliveryOutcomeUnknown(err) {
			receipt := &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, Detail: "acceptance is unconfirmed; no delivery intent was recorded"}
			if d.Registry != nil && options.SourceSessionID != "" {
				intent, intentErr := d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: options.SourceSessionID, TargetSessionID: session.ID, Message: message, Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
				if intentErr != nil {
					d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: intentErr.Error()})
				} else {
					receipt.DeliveryID = intent.ID
					receipt.Detail = "delivery intent recorded; receipt is pending"
				}
			}
			d.record(registry.HistoryEntry{Kind: "submitted", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: err.Error(), Evidence: surface.EvidenceSubmitted})
			return receipt, nil
		}
		d.record(registry.HistoryEntry{Kind: failureKind(err, "failed"), SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	if result == nil {
		err := fmt.Errorf("%s returned an empty delivery result", adapter.Name())
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	if result.Accepted {
		receipt := &Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, TurnID: result.UUID}
		peerTransport := session.Surface == surface.KindClaude && session.Transport == "uds"
		if peerTransport {
			receipt.Evidence = surface.EvidenceTransportAccepted
			receipt.Detail = "receiver policy and model completion are pending"
		}
		if d.Registry != nil && options.SourceSessionID != "" {
			intent, intentErr := d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: options.SourceSessionID, TargetSessionID: session.ID, ProviderKey: result.UUID, Message: message, Status: registry.DeliveryIntentSent, Evidence: receipt.Evidence})
			if intentErr != nil {
				d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Result: result.UUID, Error: intentErr.Error()})
			} else {
				receipt.DeliveryID = intent.ID
			}
		}
		if d.Registry != nil && !peerTransport {
			if err := d.Registry.MarkDeliveryStarted(session.ID, result.UUID, baselineCompletionID); err != nil {
				d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: message, Result: result.UUID, Error: err.Error()})
			}
		}
		if peerTransport {
			d.record(registry.HistoryEntry{Kind: "transport-accepted", SessionID: session.ID, Message: message, Result: result.UUID})
			return receipt, nil
		}
		d.record(registry.HistoryEntry{Kind: "sent", SessionID: session.ID, Message: message, Result: result.UUID})
		return receipt, nil
	}
	if !allowQueue {
		d.record(registry.HistoryEntry{Kind: "busy", SessionID: session.ID, Message: message, Error: ErrTargetBusy.Error()})
		return nil, ErrTargetBusy
	}
	if d.Registry == nil {
		err := fmt.Errorf("%s is busy and no registry is available for queuing", adapter.Name())
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	queueID, deliveryID, err := d.Registry.QueueDeliveryWithIntent(session.ID, message, deliveryKey, options)
	if err != nil {
		return nil, err
	}
	return &Receipt{Evidence: surface.EvidenceQueued, Status: string(registry.DeliveryIntentQueued), SessionID: session.ID, TurnID: result.UUID, QueueID: queueID, DeliveryID: deliveryID, Detail: "target busy"}, nil
}

func (d Dispatcher) record(entry registry.HistoryEntry) {
	if d.Registry != nil {
		_ = d.Registry.RecordHistory(entry)
	}
}

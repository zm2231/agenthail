package delivery

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
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
	Registry         *registry.Registry
	ProblemCommitted func()
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
	target := session.Name
	if target == "" {
		target = session.ID
	}
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	var intent *registry.DeliveryIntent
	if d.Registry != nil {
		sourceID := surface.SourceSessionID(ctx)
		if sourceID == "" {
			if err := d.Registry.EnsureOperatorSession(); err != nil {
				return nil, fmt.Errorf("persist operator delivery source: %w", err)
			}
			sourceID = registry.OperatorSessionID
		}
		var err error
		intent, err = d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: sourceID, TargetSessionID: session.ID, Message: message, Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
		if err != nil {
			return nil, fmt.Errorf("persist submitted delivery intent: %w", err)
		}
	}
	if err := adapter.Steer(ctx, session, message); err != nil {
		if intent != nil {
			if surface.IsDeliveryOutcomeUnknown(err) {
				return &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, DeliveryID: intent.ID, Detail: fmt.Sprintf("Submitted to %s.", target)}, nil
			}
			d.failIntent(intent, session.ID, err)
		}
		d.record(registry.HistoryEntry{Kind: failureKind(err, "control-failed"), SessionID: session.ID, Message: "steer", Error: err.Error()})
		return nil, err
	}
	receipt := &Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID}
	d.markSent(receipt, intent, "", target)
	d.record(registry.HistoryEntry{Kind: "control-accepted", SessionID: session.ID, Message: "steer"})
	return receipt, nil
}

// failureKind keeps an uncertain outcome distinct from a confirmed failure so every surface shows the same evidence.
func failureKind(err error, fallback string) string {
	if surface.IsDeliveryOutcomeUnknown(err) {
		return "unknown"
	}
	return fallback
}

func (d Dispatcher) deliver(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool) (*Receipt, error) {
	target := session.Name
	if target == "" {
		target = session.ID
	}
	claudePeer := session.Surface == surface.KindClaude && session.Transport == "uds"
	syntheticNotion := session.Surface == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
	if err := options.TurnOptions.Validate(adapter.Name()); err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if options.SourceSessionID == "" && d.Registry != nil {
		if err := d.Registry.EnsureOperatorSession(); err != nil {
			return nil, fmt.Errorf("persist operator delivery source: %w", err)
		}
		options.SourceSessionID = registry.OperatorSessionID
	}
	ctx = surface.WithSourceSessionID(ctx, options.SourceSessionID)
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	busyMode, modeErr := d.busyMode(options)
	if modeErr != nil {
		return nil, modeErr
	}
	if claudePeer {
		observation, observeErr := adapter.Observe(ctx, session)
		if observeErr != nil {
			return nil, observeErr
		}
		if observation != nil && observation.Status == surface.StatusBusy {
			var intent *registry.DeliveryIntent
			if !syntheticNotion {
				var intentErr error
				intent, intentErr = d.prepareIntent(options.SourceSessionID, session.ID, message)
				if intentErr != nil {
					return nil, intentErr
				}
			}
			return d.handleBusy(ctx, adapter, session, message, deliveryKey, options, allowQueue, busyMode, "", intent)
		}
	}
	baselineCompletionID := ""
	if d.Registry != nil && !syntheticNotion {
		state, found, stateErr := d.Registry.RuntimeState(session.ID)
		if stateErr != nil {
			d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Message: message, Error: stateErr.Error()})
		} else if found {
			baselineCompletionID = state.CompletedTurnID
		} else if observation, observeErr := adapter.Observe(ctx, session); observeErr == nil && observation != nil {
			baselineCompletionID = observation.CompletedTurnID
		}
	}
	var optionSender surface.OptionSender
	if options.Model != "" || !options.TurnOptions.Empty() {
		var ok bool
		optionSender, ok = adapter.(surface.OptionSender)
		if !ok {
			return nil, fmt.Errorf("%s does not support per-message model selection", adapter.Name())
		}
	}
	var intent *registry.DeliveryIntent
	if d.Registry != nil && !syntheticNotion {
		var intentErr error
		intent, intentErr = d.prepareIntent(options.SourceSessionID, session.ID, message)
		if intentErr != nil {
			return nil, intentErr
		}
	}
	var result *surface.SendResult
	var err error
	if optionSender != nil {
		result, err = optionSender.SendWithOptions(ctx, session, message, options)
	} else {
		result, err = adapter.Send(ctx, session, message)
	}
	if err != nil {
		if surface.IsDeliveryOutcomeUnknown(err) {
			if intent == nil {
				d.record(registry.HistoryEntry{Kind: "unknown", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: err.Error(), Evidence: surface.EvidenceUnknown})
				return nil, err
			}
			receipt := &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, DeliveryID: intent.ID, Detail: fmt.Sprintf("Submitted to %s.", target)}
			d.record(registry.HistoryEntry{Kind: "submitted", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: err.Error(), Evidence: surface.EvidenceSubmitted})
			return receipt, nil
		}
		if intent != nil {
			d.failIntent(intent, session.ID, err)
		}
		d.record(registry.HistoryEntry{Kind: failureKind(err, "failed"), SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	if result == nil {
		err := fmt.Errorf("%s returned an empty delivery result", adapter.Name())
		if intent != nil {
			return &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, DeliveryID: intent.ID, Detail: fmt.Sprintf("Submitted to %s.", target)}, nil
		}
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	if result.Accepted {
		receipt := &Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, TurnID: result.UUID}
		peerTransport := claudePeer
		if peerTransport {
			receipt.Evidence = surface.EvidenceTransportAccepted
		}
		receipt.Detail = fmt.Sprintf("Sent to %s.", target)
		d.markSent(receipt, intent, result.UUID, target)
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
	return d.handleBusy(ctx, adapter, session, message, deliveryKey, options, allowQueue, busyMode, result.UUID, intent)
}

func (d Dispatcher) prepareIntent(sourceID, targetID, message string) (*registry.DeliveryIntent, error) {
	if d.Registry == nil {
		return nil, nil
	}
	if sourceID == "" {
		if err := d.Registry.EnsureOperatorSession(); err != nil {
			return nil, fmt.Errorf("persist operator delivery source: %w", err)
		}
		sourceID = registry.OperatorSessionID
	}
	intent, err := d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: sourceID, TargetSessionID: targetID, Message: message, Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		return nil, fmt.Errorf("persist submitted delivery intent: %w", err)
	}
	return intent, nil
}

func (d Dispatcher) handleBusy(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool, busyMode deliverypolicy.Mode, turnID string, intent *registry.DeliveryIntent) (*Receipt, error) {
	target := session.Name
	if target == "" {
		target = session.ID
	}
	needsOptionSender := options.Model != "" || !options.TurnOptions.Empty()
	if busyMode == deliverypolicy.Steer && !needsOptionSender && surface.EffectiveCapabilities(session, adapter.Capabilities()).Steer {
		if steerErr := adapter.Steer(ctx, session, message); steerErr != nil {
			if intent != nil {
				if surface.IsDeliveryOutcomeUnknown(steerErr) {
					return &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, DeliveryID: intent.ID, Detail: fmt.Sprintf("Submitted to %s.", target)}, nil
				}
				d.failIntent(intent, session.ID, steerErr)
			}
			d.record(registry.HistoryEntry{Kind: failureKind(steerErr, "control-failed"), SessionID: session.ID, Message: message, Error: steerErr.Error()})
			return nil, steerErr
		}
		receipt := &Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, Detail: fmt.Sprintf("Sent to %s.", target)}
		d.markSent(receipt, intent, "", target)
		d.record(registry.HistoryEntry{Kind: "control-accepted", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: "busy steer"})
		return receipt, nil
	}
	if !allowQueue {
		d.discardIntent(intent, session.ID)
		d.record(registry.HistoryEntry{Kind: "busy", SessionID: session.ID, Message: message, Error: ErrTargetBusy.Error()})
		return nil, ErrTargetBusy
	}
	if d.Registry == nil {
		err := fmt.Errorf("%s is busy and no registry is available for queuing", adapter.Name())
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, Message: message, Error: err.Error()})
		return nil, err
	}
	if needsOptionSender {
		busyMode = deliverypolicy.Queue
	}
	options.BusyDelivery = string(busyMode)
	var queueID, deliveryID int64
	var err error
	if intent != nil {
		queueID, deliveryID, err = d.Registry.QueueDeliveryUsingIntent(intent.ID, message, deliveryKey, options)
	} else {
		queueID, deliveryID, err = d.Registry.QueueDeliveryWithIntent(session.ID, message, deliveryKey, options)
	}
	if err != nil {
		d.discardIntent(intent, session.ID)
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: fmt.Sprintf("queue storage failed: %s", err)})
		return nil, err
	}
	return &Receipt{Evidence: surface.EvidenceQueued, Status: string(registry.DeliveryIntentQueued), SessionID: session.ID, TurnID: turnID, QueueID: queueID, DeliveryID: deliveryID, Detail: fmt.Sprintf("Queued for %s; sends when current turn ends.", target)}, nil
}

// failIntent publishes a durable problem and sender notice only when the
// provider may have seen the delivery; a refusal before anything started is
// already returned synchronously and leaves only the history audit.
func (d Dispatcher) failIntent(intent *registry.DeliveryIntent, sessionID string, failure error) {
	if surface.IsDeliveryUnavailable(failure) {
		d.discardIntent(intent, sessionID)
		return
	}
	if _, err := d.Registry.FailDeliveryIntentWithNotice(intent.ID, failure.Error()); err != nil {
		d.record(registry.HistoryEntry{Kind: "runtime-error", Error: fmt.Sprintf("record delivery %d failure: %s", intent.ID, err)})
	}
	if d.ProblemCommitted != nil {
		d.ProblemCommitted()
	}
}

func (d Dispatcher) discardIntent(intent *registry.DeliveryIntent, sessionID string) {
	if intent == nil {
		return
	}
	if err := d.Registry.DiscardDeliveryIntent(intent.ID); err != nil {
		d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: sessionID, Error: fmt.Sprintf("discard refused delivery %d: %s", intent.ID, err)})
	}
}

// markSent keeps the receipt equal to the durable intent: when the sent
// transition cannot persist, callers still finish post-effect bookkeeping but
// report the stored submitted state.
func (d Dispatcher) markSent(receipt *Receipt, intent *registry.DeliveryIntent, providerKey, target string) {
	if intent == nil {
		return
	}
	receipt.DeliveryID = intent.ID
	changed, err := d.Registry.MarkDeliveryIntentSent(intent.ID, providerKey, receipt.Evidence)
	if err == nil && changed {
		return
	}
	if err == nil {
		err = fmt.Errorf("intent was no longer submitted")
	}
	d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: receipt.SessionID, Result: providerKey, Error: fmt.Sprintf("record sent delivery %d: %s", intent.ID, err)})
	receipt.Evidence = surface.EvidenceSubmitted
	receipt.Status = string(registry.DeliveryIntentSubmitted)
	receipt.Detail = fmt.Sprintf("Submitted to %s.", target)
}

func (d Dispatcher) busyMode(options surface.SendOptions) (deliverypolicy.Mode, error) {
	if options.BusyDelivery != "" {
		return deliverypolicy.Normalize(options.BusyDelivery)
	}
	return deliverypolicy.Load()
}

func (d Dispatcher) record(entry registry.HistoryEntry) {
	if d.Registry != nil {
		_ = d.Registry.RecordHistory(entry)
	}
}

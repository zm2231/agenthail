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
	RequestID  string                   `json:"requestId,omitempty"`
	Source     string                   `json:"source,omitempty"`
	TurnID     string                   `json:"turnId,omitempty"`
	QueueID    int64                    `json:"queueId,omitempty"`
	DeliveryID int64                    `json:"deliveryId,omitempty"`
	Detail     string                   `json:"detail,omitempty"`
	Notice     string                   `json:"notice,omitempty"`
	// WaitingOn is set on a queued receipt when the target is waiting on its
	// user; the message is delivered after the user answers.
	WaitingOn *surface.Waiting `json:"waitingOn,omitempty"`
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

// GoalSet sets the session's goal, or with replace changes only its
// objective. A session that sets its goal by command holds it until idle.
func (d Dispatcher) GoalSet(ctx context.Context, adapter surface.Surface, session *surface.Session, objective string, replace bool) (*Receipt, error) {
	objective = strings.TrimSpace(objective)
	if objective == "" {
		return nil, surface.DeliveryTerminal(errors.New("goal objective is required"), surface.DeliveryInvalidRequest)
	}
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	if err := goalSettable(adapter, session); err != nil {
		return nil, err
	}
	if receipt, held, err := d.holdGoalCommand(adapter, session, objective); held {
		return receipt, err
	}
	var err error
	if controller, ok := adapter.(surface.GoalController); ok {
		update := surface.GoalUpdate{Objective: &objective}
		if !replace {
			status := surface.GoalStatusActive
			update.Status = &status
		}
		err = controller.UpdateGoal(ctx, session, update)
	} else {
		err = adapter.GoalSet(ctx, session, objective)
	}
	return d.goalReceipt(session, "goal "+objective, err)
}

func (d Dispatcher) GoalClear(ctx context.Context, adapter surface.Surface, session *surface.Session) (*Receipt, error) {
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	if err := goalSettable(adapter, session); err != nil {
		return nil, err
	}
	if receipt, held, err := d.holdGoalCommand(adapter, session, ""); held {
		return receipt, err
	}
	return d.goalReceipt(session, "goal clear", adapter.GoalClear(ctx, session))
}

// goalSettable refuses a session whose goal is not set here, with the
// surface's own reason when it has one.
func goalSettable(adapter surface.Surface, session *surface.Session) error {
	if surface.EffectiveCapabilities(adapter, session).GoalSet {
		return nil
	}
	if runner, ok := adapter.(surface.GoalCommandRunner); ok {
		if _, err := runner.GoalCommand(session, ""); err != nil {
			return err
		}
	}
	return surface.DeliveryTerminal(fmt.Errorf("%s cannot set or clear this session's goal", adapter.Name()), surface.DeliveryInvalidRequest)
}

func (d Dispatcher) holdGoalCommand(adapter surface.Surface, session *surface.Session, objective string) (*Receipt, bool, error) {
	runner, ok := adapter.(surface.GoalCommandRunner)
	if !ok || d.Registry == nil {
		return nil, false, nil
	}
	command, err := runner.GoalCommand(session, objective)
	if err != nil {
		return nil, true, err
	}
	queueID, err := d.Registry.QueueGoal(session.ID, command)
	if err != nil {
		return nil, true, err
	}
	return &Receipt{Evidence: surface.EvidenceQueued, SessionID: session.ID, QueueID: queueID, Detail: command + " will run when the target is idle"}, true, nil
}

func (d Dispatcher) goalReceipt(session *surface.Session, label string, err error) (*Receipt, error) {
	if err != nil {
		d.record(registry.HistoryEntry{Kind: failureKind(err, "control-failed"), SessionID: session.ID, Message: label, Error: err.Error()})
		return nil, err
	}
	d.record(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, Message: label, Evidence: surface.EvidenceDelivered})
	return &Receipt{Evidence: surface.EvidenceDelivered, SessionID: session.ID}, nil
}

func (d Dispatcher) Steer(ctx context.Context, adapter surface.Surface, session *surface.Session, message string, author surface.Author) (*Receipt, error) {
	target := session.Name
	if target == "" {
		target = session.ID
	}
	sourceID := surface.SendOptions{SourceSessionID: surface.SourceSessionID(ctx), Author: author}.Sender()
	if d.Registry != nil {
		if err := d.Registry.EnsureSender(sourceID); err != nil {
			return nil, fmt.Errorf("persist delivery source: %w", err)
		}
	}
	ctx = surface.WithSourceSessionID(ctx, sourceID)
	if err := surface.EnsureWritableSession(ctx, adapter, session); err != nil {
		return nil, err
	}
	observation, err := adapter.Observe(ctx, session)
	if err != nil {
		return nil, err
	}
	if observation != nil && observation.WaitingOn != nil && d.Registry != nil {
		return d.queueForWaiting(session, message, target, sourceID, observation.WaitingOn)
	}
	var intent *registry.DeliveryIntent
	if d.Registry != nil {
		var err error
		intent, err = d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: sourceID, TargetSessionID: session.ID, Message: message, Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
		if err != nil {
			return nil, fmt.Errorf("persist submitted delivery intent: %w", err)
		}
		ctx = surface.WithMessageUUID(ctx, intent.UUID)
	}
	return d.steer(ctx, adapter, session, message, target, "", "steer", intent, false)
}

// steer reports the evidence the surface proved: a Claude Remote Control steer
// is accepted by the transport, which does not show it reached the turn.
func (d Dispatcher) steer(ctx context.Context, adapter surface.Surface, session *surface.Session, message, target, sourceID, label string, intent *registry.DeliveryIntent, sendIfEnded bool) (*Receipt, error) {
	result, err := surface.SteerWithEvidence(ctx, adapter, session, message)
	if sendIfEnded && errors.Is(err, surface.ErrNothingToSteer) {
		return nil, errTurnEnded
	}
	if err != nil {
		if intent != nil {
			if surface.IsDeliveryOutcomeUnknown(err) {
				return &Receipt{Evidence: surface.EvidenceSubmitted, Status: string(registry.DeliveryIntentSubmitted), SessionID: session.ID, DeliveryID: intent.ID, Detail: fmt.Sprintf("Submitted to %s.", target)}, nil
			}
			d.failIntent(intent, session.ID, err)
		}
		d.record(registry.HistoryEntry{Kind: failureKind(err, "control-failed"), SessionID: session.ID, SourceSessionID: sourceID, Message: label, Error: err.Error()})
		return nil, err
	}
	receipt := &Receipt{Evidence: result.Evidence, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, RequestID: result.RequestID, Source: result.Source, TurnID: result.TurnID, Detail: fmt.Sprintf("Sent to %s.", target)}
	kind := "control-accepted"
	if result.Evidence == surface.EvidenceTransportAccepted {
		kind = "transport-accepted"
		receipt.Detail = fmt.Sprintf("Steer accepted for %s; not yet confirmed in the current turn.", target)
	}
	d.markSent(receipt, intent, result.RequestID, target)
	d.record(registry.HistoryEntry{Kind: kind, SessionID: session.ID, SourceSessionID: sourceID, Message: label, Result: result.RequestID})
	return receipt, nil
}

// queueForWaiting holds a steer for a turn that is waiting on its user, so the
// message cannot land in the middle of the question. It steers once answered.
func (d Dispatcher) queueForWaiting(session *surface.Session, message, target, sourceID string, waiting *surface.Waiting) (*Receipt, error) {
	options := surface.SendOptions{SourceSessionID: sourceID, BusyDelivery: string(deliverypolicy.Steer)}
	queueID, deliveryID, err := d.Registry.QueueDeliveryWithIntent(session.ID, message, "", options)
	if err != nil {
		d.record(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, SourceSessionID: sourceID, Message: message, Error: fmt.Sprintf("queue storage failed: %s", err)})
		return nil, err
	}
	return &Receipt{Evidence: surface.EvidenceQueued, Status: string(registry.DeliveryIntentQueued), SessionID: session.ID, QueueID: queueID, DeliveryID: deliveryID, Detail: fmt.Sprintf("Queued for %s, which is waiting on you; sends after you answer.", target), WaitingOn: waiting}, nil
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
	route := surface.RouteFor(adapter, session, options.Author)
	syntheticNotion := session.Surface == surface.KindNotion && (session.ID == "new" || strings.HasPrefix(session.ID, "new:"))
	if err := options.TurnOptions.Validate(adapter.Name()); err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	if d.Registry != nil {
		options.SourceSessionID = options.Sender()
		if err := d.Registry.EnsureSender(options.SourceSessionID); err != nil {
			return nil, fmt.Errorf("persist delivery source: %w", err)
		}
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
	var intent *registry.DeliveryIntent
	retry := true
	// A Claude peer socket delivers to a running or waiting turn with the
	// sender's address, so only an explicit queue or a message that needs
	// per-message options waits for the turn to end.
	if route == surface.RoutePeer && (options.BusyDelivery != "" && busyMode == deliverypolicy.Queue || options.HoldsForQueue()) {
		observation, observeErr := adapter.Observe(ctx, session)
		if observeErr != nil {
			return nil, observeErr
		}
		if observation != nil && observation.Status == surface.StatusBusy {
			if !syntheticNotion {
				var intentErr error
				intent, intentErr = d.prepareIntent(options.SourceSessionID, session.ID, message)
				if intentErr != nil {
					return nil, intentErr
				}
				if intent != nil {
					ctx = surface.WithMessageUUID(ctx, intent.UUID)
				}
			}
			receipt, err := d.handleBusy(ctx, adapter, session, message, deliveryKey, options, allowQueue, busyMode, "", intent, observation)
			if !errors.Is(err, errTurnEnded) {
				return receipt, err
			}
			retry = false
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
	if err := route.Validate(adapter, options); err != nil {
		return nil, err
	}
	if intent == nil && d.Registry != nil && !syntheticNotion {
		var intentErr error
		intent, intentErr = d.prepareIntent(options.SourceSessionID, session.ID, message)
		if intentErr != nil {
			return nil, intentErr
		}
		if intent != nil {
			ctx = surface.WithMessageUUID(ctx, intent.UUID)
		}
	}
	return d.send(ctx, adapter, session, route, message, deliveryKey, options, allowQueue, busyMode, intent, baselineCompletionID, target, retry)
}

// send delivers message as the session's next turn. A busy session is
// steered or queued; when the steer finds the turn already over, the message
// is sent once more as the next turn, and queued if the session is busy again.
func (d Dispatcher) send(ctx context.Context, adapter surface.Surface, session *surface.Session, route surface.Route, message, deliveryKey string, options surface.SendOptions, allowQueue bool, busyMode deliverypolicy.Mode, intent *registry.DeliveryIntent, baselineCompletionID, target string, retry bool) (*Receipt, error) {
	addressed := *session
	result, err := route.Send(ctx, adapter, session, message, options)
	if ownedErr := d.RegisterReached(&addressed, session); ownedErr != nil {
		*session = addressed
		d.record(registry.HistoryEntry{Kind: "unknown", SessionID: addressed.ID, SourceSessionID: options.SourceSessionID, Message: message, Error: ownedErr.Error(), Evidence: surface.EvidenceUnknown})
		return nil, ownedErr
	}
	if surface.IsDeliveryHeld(err) {
		options.BusyDelivery = string(deliverypolicy.Queue)
		return d.handleBusy(ctx, adapter, session, message, deliveryKey, options, allowQueue, deliverypolicy.Queue, "", intent, nil)
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
		receipt := &Receipt{Evidence: surface.EvidenceDelivered, Status: string(registry.DeliveryIntentSent), SessionID: session.ID, TurnID: result.UUID, Notice: result.Notice}
		peerTransport := surface.RouteFor(adapter, session, options.Author).TransportOnly()
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
		if result.Notice != "" {
			d.record(registry.HistoryEntry{Kind: "model", SessionID: session.ID, Message: result.Notice, Result: result.UUID})
		}
		return receipt, nil
	}
	if !retry || route == surface.RoutePeer {
		busyMode = deliverypolicy.Queue
		options.BusyDelivery = string(deliverypolicy.Queue)
	}
	receipt, err := d.handleBusy(ctx, adapter, session, message, deliveryKey, options, allowQueue, busyMode, result.UUID, intent, nil)
	if errors.Is(err, errTurnEnded) {
		return d.send(ctx, adapter, session, route, message, deliveryKey, options, allowQueue, busyMode, intent, baselineCompletionID, target, false)
	}
	return receipt, err
}

func (d Dispatcher) prepareIntent(sourceID, targetID, message string) (*registry.DeliveryIntent, error) {
	if d.Registry == nil {
		return nil, nil
	}
	intent, err := d.Registry.RecordDeliveryIntent(registry.DeliveryIntentInput{SenderSessionID: sourceID, TargetSessionID: targetID, Message: message, Status: registry.DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted})
	if err != nil {
		return nil, fmt.Errorf("persist submitted delivery intent: %w", err)
	}
	return intent, nil
}

// errTurnEnded is handleBusy finding the target's turn over by the time it
// steered; the caller sends the message as the next turn instead.
var errTurnEnded = errors.New("the busy turn ended before the steer")

// handleBusy steers or queues a message for a busy target. observation is the
// target's state when the caller already has it; without it, a steer observes
// the target first. A target waiting on its user is never steered: the
// message is queued and delivered after the user answers.
func (d Dispatcher) handleBusy(ctx context.Context, adapter surface.Surface, session *surface.Session, message, deliveryKey string, options surface.SendOptions, allowQueue bool, busyMode deliverypolicy.Mode, turnID string, intent *registry.DeliveryIntent, observation *surface.TurnObservation) (*Receipt, error) {
	target := session.Name
	if target == "" {
		target = session.ID
	}
	needsOptionSender := options.HoldsForQueue()
	steerable := busyMode == deliverypolicy.Steer && !needsOptionSender && surface.EffectiveCapabilities(adapter, session).Steer
	if steerable && observation == nil {
		observed, observeErr := adapter.Observe(ctx, session)
		if observeErr != nil {
			d.discardIntent(intent, session.ID)
			return nil, observeErr
		}
		observation = observed
	}
	var waiting *surface.Waiting
	if observation != nil {
		waiting = observation.WaitingOn
	} else if d.Registry != nil {
		recorded, err := d.Registry.SessionWaiting(session.ID)
		if err != nil {
			d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: session.ID, Error: fmt.Sprintf("read waiting state: %s", err)})
		}
		waiting = recorded
	}
	if steerable && waiting == nil {
		return d.steer(ctx, adapter, session, message, target, options.SourceSessionID, "busy steer", intent, true)
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
		options.BusyDelivery = string(deliverypolicy.Queue)
	}
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
	receipt := &Receipt{Evidence: surface.EvidenceQueued, Status: string(registry.DeliveryIntentQueued), SessionID: session.ID, TurnID: turnID, QueueID: queueID, DeliveryID: deliveryID, Detail: fmt.Sprintf("Queued for %s; sends when current turn ends.", target), WaitingOn: waiting}
	if waiting != nil {
		receipt.Detail = fmt.Sprintf("Queued for %s, which is waiting on you; sends after you answer.", target)
	}
	return receipt, nil
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

// RegisterReached records the process a send reached when the surface moved
// the session to another one, so registration carries the old row's alias,
// queue and intents over to it. When a paired host owns the reached id, the
// provider already took the message but this daemon cannot track it: the
// returned error is an unknown outcome naming that host, and the caller must
// not record anything under the reached id.
func (d Dispatcher) RegisterReached(addressed, reached *surface.Session) error {
	if d.Registry == nil || (addressed.ID == reached.ID && addressed.PID == reached.PID && addressed.Transport == reached.Transport) {
		return nil
	}
	err := d.Registry.RegisterSession(*reached)
	var owned registry.SessionOwnedError
	if errors.As(err, &owned) {
		host := owned.HostID
		if paired, hostErr := d.Registry.Host(owned.HostID); hostErr == nil {
			host = paired.Name
		}
		return surface.DeliveryOutcomeUnknown(fmt.Errorf("%s resumed as session %s, which paired host %s owns; this daemon cannot follow it", addressed.ID, reached.ID, host))
	}
	if err != nil {
		d.record(registry.HistoryEntry{Kind: "runtime-error", SessionID: reached.ID, Error: fmt.Sprintf("register reached session: %s", err)})
	}
	return nil
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

// RecordControl records a control's request id under the history kind that
// reads back as the control's evidence.
func (d Dispatcher) RecordControl(result *surface.ControlResult, controlErr error) {
	if result == nil {
		return
	}
	kind := map[surface.DeliveryEvidence]string{
		surface.EvidenceTransportAccepted: "transport-accepted",
		surface.EvidenceDelivered:         "control-accepted",
		surface.EvidenceReplyObserved:     "reply",
		surface.EvidenceFailed:            "control-failed",
	}[result.Evidence]
	if kind == "" {
		kind = "unknown"
	}
	errorText := ""
	if controlErr != nil {
		errorText = controlErr.Error()
	}
	d.record(registry.HistoryEntry{Kind: kind, SessionID: result.SessionID, Message: result.Action, Result: result.RequestID, Error: errorText})
}

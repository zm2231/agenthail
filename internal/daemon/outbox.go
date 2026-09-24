package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const maxDeliveryAttempts = 5

const compactOperationTimeout = 5 * time.Minute

func (d *Daemon) drainMessageQueue(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	now := time.Now()
	item, err := d.Registry.ClaimNextMessage(session.ID, now)
	if err != nil {
		d.log.Printf("claim queue for %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if item == nil {
		return
	}
	if item.Operation == registry.QueueOperationCompact {
		d.queueWorkers.Add(1)
		go func() {
			defer d.queueWorkers.Done()
			d.runQueuedCompact(ctx, adapter, session, item)
		}()
		return
	}
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	operationCtx = surface.WithSourceSessionID(operationCtx, item.SourceSessionID)
	var result *surface.SendResult
	var sendErr error
	if item.Model != "" || !item.TurnOptions.Empty() {
		if sender, ok := adapter.(surface.OptionSender); ok {
			result, sendErr = sender.SendWithOptions(operationCtx, session, item.Message, surface.SendOptions{Model: item.Model, SourceSessionID: item.SourceSessionID, TurnOptions: item.TurnOptions})
		} else {
			sendErr = fmt.Errorf("%s does not support per-message model selection", adapter.Name())
		}
	} else {
		result, sendErr = adapter.Send(operationCtx, session, item.Message)
	}
	cancel()
	if sendErr != nil {
		d.finishQueueFailure(item, session, sendErr, now)
		return
	}
	if result == nil || !result.Accepted {
		busyErr := fmt.Errorf("target remained busy")
		if err := d.Registry.NackMessage(item.ID, busyErr, now, maxDeliveryAttempts); err != nil {
			d.log.Printf("nack queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "busy", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Error: busyErr.Error()})
		return
	}
	if session.Surface != surface.KindClaude || session.Transport != "uds" {
		if err := d.Registry.MarkDeliveryStarted(session.ID, result.UUID, ""); err != nil {
			d.log.Printf("record queue delivery %d start: %s", item.ID, err)
		}
	}
	evidence := surface.EvidenceDelivered
	historyKind := "delivered"
	if session.Surface == surface.KindClaude && session.Transport == "uds" {
		evidence = surface.EvidenceTransportAccepted
		historyKind = "transport-accepted"
	}
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, evidence); err != nil {
		d.log.Printf("ack queue item %d: %s", item.ID, err)
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "ack-error", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Result: result.UUID, Error: err.Error()})
		return
	}
	d.log.Printf("delivered queue item %d to %s", item.ID, d.resolveDisplay(session.ID))
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: historyKind, SessionID: session.ID, QueueID: item.ID, Message: item.Message, Result: result.UUID})
}

func (d *Daemon) runQueuedCompact(ctx context.Context, adapter surface.Surface, session *surface.Session, item *registry.QueuedMessage) {
	defer d.publishEvent("state.changed", session.ID, map[string]string{"source": "compact"})
	operationCtx, cancel := context.WithTimeout(ctx, compactOperationTimeout)
	defer cancel()
	err := adapter.Compact(operationCtx, session)
	if err != nil {
		d.finishQueueFailure(item, session, err, time.Now())
		return
	}
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, surface.EvidenceDelivered); err != nil {
		d.log.Printf("ack compact queue item %d: %s", item.ID, err)
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "ack-error", SessionID: session.ID, QueueID: item.ID, Message: "compact", Error: err.Error()})
		return
	}
	d.log.Printf("completed compact queue item %d for %s", item.ID, d.resolveDisplay(session.ID))
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, QueueID: item.ID, Message: "compact", Evidence: surface.EvidenceDelivered})
}

func (d *Daemon) finishQueueFailure(item *registry.QueuedMessage, session *surface.Session, sendErr error, now time.Time) {
	message := item.Message
	if item.Operation != registry.QueueOperationMessage {
		message = string(item.Operation)
	}
	if surface.IsDeliveryUnavailable(sendErr) {
		if err := d.Registry.DeferMessage(item.ID, sendErr, now); err != nil {
			d.log.Printf("defer queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "deferred", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
		return
	}
	if surface.IsDeliveryOutcomeUnknown(sendErr) {
		if err := d.Registry.DeadLetterUnknown(item.ID, sendErr); err != nil {
			d.log.Printf("dead-letter uncertain queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "unknown", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
		return
	}
	if surface.IsDeliveryTerminal(sendErr) {
		if err := d.Registry.DeadLetterMessage(item.ID, sendErr); err != nil {
			d.log.Printf("dead-letter rejected queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
		return
	}
	if err := d.Registry.NackMessage(item.ID, sendErr, now, maxDeliveryAttempts); err != nil {
		d.log.Printf("nack queue item %d: %s", item.ID, err)
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
}

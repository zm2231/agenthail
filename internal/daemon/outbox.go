package daemon

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const maxDeliveryAttempts = 5

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
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	var result *surface.SendResult
	var sendErr error
	if item.Model != "" {
		if sender, ok := adapter.(surface.OptionSender); ok {
			result, sendErr = sender.SendWithOptions(operationCtx, session, item.Message, surface.SendOptions{Model: item.Model})
		} else {
			sendErr = fmt.Errorf("%s does not support per-message model selection", adapter.Name())
		}
	} else {
		result, sendErr = adapter.Send(operationCtx, session, item.Message)
	}
	cancel()
	if sendErr != nil {
		if surface.IsDeliveryOutcomeUnknown(sendErr) {
			if err := d.Registry.DeadLetterUnknown(item.ID, sendErr); err != nil {
				d.log.Printf("dead-letter uncertain queue item %d: %s", item.ID, err)
			}
			d.log.Printf("queue delivery %d has unknown outcome and requires explicit retry: %s", item.ID, sendErr)
			_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "unknown", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Error: sendErr.Error()})
			return
		}
		if err := d.Registry.NackMessage(item.ID, sendErr, now, maxDeliveryAttempts); err != nil {
			d.log.Printf("nack queue item %d: %s", item.ID, err)
		}
		d.log.Printf("queue delivery %d failed: %s", item.ID, sendErr)
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Error: sendErr.Error()})
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
	if err := d.Registry.AckMessage(item.ID); err != nil {
		d.log.Printf("ack queue item %d: %s", item.ID, err)
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "ack-error", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Result: result.UUID, Error: err.Error()})
		return
	}
	d.log.Printf("delivered queue item %d to %s", item.ID, d.resolveDisplay(session.ID))
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Result: result.UUID})
}

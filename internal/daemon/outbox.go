package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const maxDeliveryAttempts = 5

const compactOperationTimeout = 5 * time.Minute

func (d *Daemon) drainMessageQueue(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.drainClaimedMessage(ctx, adapter, session, func(now time.Time) (*registry.QueuedMessage, error) {
		return d.Registry.ClaimNextMessage(session.ID, now)
	})
}

func (d *Daemon) drainSteerMessageQueue(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.drainClaimedMessage(ctx, adapter, session, func(now time.Time) (*registry.QueuedMessage, error) {
		return d.Registry.ClaimNextSteerMessage(session.ID, now)
	})
}

// drainPeerMessageQueue sends the session's next peer message over its Claude
// peer socket while a turn runs or waits on its user.
func (d *Daemon) drainPeerMessageQueue(ctx context.Context, adapter surface.Surface, session *surface.Session) {
	d.drainClaimedMessage(ctx, adapter, session, func(now time.Time) (*registry.QueuedMessage, error) {
		return d.Registry.ClaimNextPeerMessage(session.ID, now)
	})
}

func (d *Daemon) drainClaimedMessage(ctx context.Context, adapter surface.Surface, session *surface.Session, claim func(time.Time) (*registry.QueuedMessage, error)) {
	now := time.Now()
	item, err := claim(now)
	if err != nil {
		d.log.Printf("claim queue for %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if item == nil {
		return
	}
	if item.Operation == registry.QueueOperationCompact {
		// The compact may resume the session and move it to another process;
		// it works on its own copy so the observer's session is never written
		// from this goroutine.
		compacting := *session
		d.queueWorkers.Add(1)
		go func() {
			defer d.queueWorkers.Done()
			d.runQueuedCompact(ctx, adapter, &compacting, item)
		}()
		return
	}
	operationCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	operationCtx = surface.WithSourceSessionID(operationCtx, item.SourceSessionID)
	if messageUUID, err := d.Registry.DeliveryIntentUUIDForQueue(item.ID); err != nil {
		d.log.Printf("read delivery intent of queue item %d: %s", item.ID, err)
	} else if messageUUID != "" {
		operationCtx = surface.WithMessageUUID(operationCtx, messageUUID)
	}
	var result *surface.SendResult
	var sendErr error
	addressed := *session
	var steered *surface.ControlResult
	route := surface.RouteFor(adapter, session, item.Author)
	options := surface.SendOptions{Model: item.Model, SourceSessionID: item.SourceSessionID, TurnOptions: item.TurnOptions, Author: item.Author, Uploads: item.Uploads}
	steers, policyErr := d.Registry.Steers(item.BusyDelivery)
	if policyErr != nil {
		d.log.Printf("read busy delivery policy for queue item %d: %s", item.ID, policyErr)
	}
	steer := steers && !options.HoldsForQueue() && session.Status == surface.StatusBusy && session.WaitingOn == nil && surface.EffectiveCapabilities(adapter, session).Steer
	if steer {
		steered, sendErr = surface.SteerWithEvidence(operationCtx, adapter, session, item.Message)
		if sendErr == nil {
			result = &surface.SendResult{UUID: steered.RequestID, Accepted: true}
		}
	}
	if !steer || errors.Is(sendErr, surface.ErrNothingToSteer) {
		steered = nil
		if sendErr = route.Validate(adapter, options); sendErr == nil {
			result, sendErr = route.Send(operationCtx, adapter, session, item.Message, options)
		}
	}
	cancel()
	if ownedErr := d.dispatcher().RegisterReached(&addressed, session); ownedErr != nil {
		*session = addressed
		d.finishReachedHostOwned(item, session, item.Message, ownedErr)
		return
	}
	peerTransport := surface.RouteFor(adapter, session, item.Author).TransportOnly()
	if sendErr != nil {
		d.finishQueueFailure(item, session, sendErr, now)
		return
	}
	if result == nil {
		unknownErr := fmt.Errorf("provider returned empty delivery result")
		if err := d.Registry.DeadLetterUnknown(item.ID, unknownErr); err != nil {
			d.log.Printf("dead-letter uncertain queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "unknown", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Error: unknownErr.Error()})
		return
	}
	if !result.Accepted {
		busyErr := fmt.Errorf("target remained busy")
		if _, err := d.Registry.DeferMessage(item.ID, busyErr, now); err != nil {
			d.log.Printf("defer queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "busy", SessionID: session.ID, QueueID: item.ID, Message: item.Message, Error: busyErr.Error()})
		return
	}
	if steered == nil && !peerTransport {
		if err := d.Registry.MarkDeliveryStarted(session.ID, result.UUID, ""); err != nil {
			d.log.Printf("record queue delivery %d start: %s", item.ID, err)
		}
	}
	evidence := surface.EvidenceDelivered
	historyKind := "delivered"
	if peerTransport || steered != nil && steered.Evidence == surface.EvidenceTransportAccepted {
		evidence = surface.EvidenceTransportAccepted
		historyKind = "transport-accepted"
	}
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, evidence, result.UUID); err != nil {
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
	addressed := *session
	err := adapter.Compact(operationCtx, session)
	if ownedErr := d.dispatcher().RegisterReached(&addressed, session); ownedErr != nil {
		d.finishReachedHostOwned(item, &addressed, "compact", ownedErr)
		return
	}
	if err != nil {
		d.finishQueueFailure(item, session, err, time.Now())
		return
	}
	if err := d.Registry.AckMessageWithEvidence(item.ID, session.ID, item.RelayHops, surface.EvidenceDelivered, ""); err != nil {
		d.log.Printf("ack compact queue item %d: %s", item.ID, err)
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "ack-error", SessionID: session.ID, QueueID: item.ID, Message: "compact", Error: err.Error()})
		return
	}
	d.log.Printf("completed compact queue item %d for %s", item.ID, d.resolveDisplay(session.ID))
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "delivered", SessionID: session.ID, QueueID: item.ID, Message: "compact", Evidence: surface.EvidenceDelivered})
}

// finishReachedHostOwned settles a queue item whose send resumed into a
// session a paired host owns. The provider took it, so it is never retried,
// and it is recorded only under the session it was addressed to.
func (d *Daemon) finishReachedHostOwned(item *registry.QueuedMessage, addressed *surface.Session, message string, ownedErr error) {
	if err := d.Registry.DeadLetterUnknown(item.ID, ownedErr); err != nil {
		d.log.Printf("dead-letter queue item %d: %s", item.ID, err)
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "unknown", SessionID: addressed.ID, QueueID: item.ID, Message: message, Error: ownedErr.Error(), Evidence: surface.EvidenceUnknown})
}

func (d *Daemon) finishQueueFailure(item *registry.QueuedMessage, session *surface.Session, sendErr error, now time.Time) {
	message := item.Message
	if item.Operation != registry.QueueOperationMessage {
		message = string(item.Operation)
	}
	if surface.IsDeliveryUnavailable(sendErr) {
		refusal, err := d.Registry.DeferMessage(item.ID, sendErr, now)
		if err != nil {
			d.log.Printf("defer queue item %d: %s", item.ID, err)
		}
		if refusal != 0 {
			d.announceRefusal(session.ID, item.ID, refusal)
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
		notified, err := d.Registry.DeadLetterMessage(item.ID, sendErr)
		if err != nil {
			d.log.Printf("dead-letter rejected queue item %d: %s", item.ID, err)
		}
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
		if notified {
			if err := d.catalog.flushCommitted(); err != nil {
				d.log.Printf("publish terminal delivery problem: %s", err)
			}
			d.publishEvent("state.changed", session.ID, map[string]string{"source": "delivery-problem"})
		}
		return
	}
	if err := d.Registry.NackMessage(item.ID, sendErr, now, maxDeliveryAttempts); err != nil {
		d.log.Printf("nack queue item %d: %s", item.ID, err)
	}
	_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "failed", SessionID: session.ID, QueueID: item.ID, Message: message, Error: sendErr.Error()})
}

// announceRefusal tells the user, on the desktop and on paired devices, that a
// queued message waits because its target refused to start. The queue row and
// its attention item carry the reason. The refusal is marked announced only
// after both were attempted, so a daemon stopped in between announces it on
// the next refused attempt rather than never; both carry a key per message, so
// a repeat replaces the one already shown.
func (d *Daemon) announceRefusal(sessionID string, queueID, refusal int64) {
	d.announceMu.Lock()
	if d.announcingRefusal == nil {
		d.announcingRefusal = map[int64]int64{}
	}
	if d.announcingRefusal[queueID] == refusal {
		d.announceMu.Unlock()
		return
	}
	d.announcingRefusal[queueID] = refusal
	d.announceMu.Unlock()
	desktopMessage := fmt.Sprintf("%s refused to start; a queued message is waiting", d.resolveDisplay(sessionID))
	key := fmt.Sprintf("refused-%d", queueID)
	go func() {
		defer func() {
			d.announceMu.Lock()
			if d.announcingRefusal[queueID] == refusal {
				delete(d.announcingRefusal, queueID)
			}
			d.announceMu.Unlock()
		}()
		if err := Notify("Agenthail", desktopMessage, sessionID, "agenthail-"+key); err != nil {
			d.log.Printf("desktop notification: %s", err)
		}
		notificationCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		d.notifyPairedDevices(notificationCtx, mobileNotification{Title: "Agenthail", Message: "A session refused to start; a queued message is waiting", SessionID: sessionID, EventType: "delivery.refused", CollapseID: key})
		if err := d.Registry.MarkRefusalAnnounced(queueID, refusal); err != nil {
			d.log.Printf("mark refusal announced %d: %s", queueID, err)
		}
	}()
}

package registry

import (
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/deliverypolicy"
	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type QueueRow struct {
	surface.TurnOptions
	ID              int64                    `json:"id"`
	SessionID       string                   `json:"sessionId"`
	Message         string                   `json:"message"`
	Model           string                   `json:"model,omitempty"`
	SourceSessionID string                   `json:"sourceSessionId,omitempty"`
	Status          string                   `json:"status"`
	Attempts        int                      `json:"attempts"`
	LastError       string                   `json:"lastError,omitempty"`
	QueuedAt        string                   `json:"queuedAt"`
	DeadAt          int64                    `json:"-"`
	HeldBy          QueueHold                `json:"heldBy,omitempty"`
	Historical      bool                     `json:"historical"`
	Evidence        surface.DeliveryEvidence `json:"evidence"`
	Operation       QueueOperation           `json:"operation"`
	BusyDelivery    string                   `json:"busyDelivery,omitempty"`
}

type AttentionItem struct {
	ID              int64  `json:"id"`
	SessionID       string `json:"sessionId"`
	QueueID         int64  `json:"queueId"`
	Reason          string `json:"reason"`
	RequestedAction string `json:"requestedAction"`
	CreatedAt       string `json:"createdAt"`
	ResolvedAt      string `json:"resolvedAt,omitempty"`
	Resolution      string `json:"resolution,omitempty"`
}

const QueueRetryBackoff = 5 * time.Second

const uncertainDeliveryError = "delivery outcome is unknown after daemon interruption; retry explicitly if the target did not receive it"

type QueueHold string

const (
	QueueHeldWaiting         QueueHold = "waiting"
	QueueHeldBusy            QueueHold = "busy"
	QueueHeldOffline         QueueHold = "offline"
	QueueHeldUnknown         QueueHold = "unknown"
	QueueHeldRefused         QueueHold = "refused"
	QueueHeldRetrying        QueueHold = "retrying"
	QueueHeldBehind          QueueHold = "behind"
	QueueHeldUnloaded        QueueHold = "notLoaded"
	QueueHeldLimited         QueueHold = "limited"
	QueueHeldHostUnreachable QueueHold = "hostUnreachable"
)

const queueTargetStatus = `CASE WHEN s.id IS NOT NULL AND (s.status='offline' OR NOT EXISTS (SELECT 1 FROM catalog_sessions cs WHERE cs.session_id=s.id)) THEN CASE WHEN s.surface='claude' AND s.pid>0 THEN 'idle' ELSE 'offline' END ELSE COALESCE(CASE WHEN sr.turn_observed=1 THEN sr.last_status END,s.status,'unknown') END`

const queueToPeerSocket = `%[1]s.author!='person' AND EXISTS (SELECT 1 FROM sessions ps WHERE ps.id=%[1]s.session_id AND ps.surface='claude' AND ps.transport='uds')`

func queueSteerRow(alias string, mode deliverypolicy.Mode) string {
	choice := `%[1]s.busy_delivery='steer'`
	if mode == deliverypolicy.Steer {
		choice = `(%[1]s.busy_delivery='steer' OR %[1]s.busy_delivery='' AND NOT (` + queueToPeerSocket + `))`
	}
	return fmt.Sprintf(choice+` AND %[1]s.operation='message' AND %[1]s.model='' AND (%[1]s.turn_options='' OR %[1]s.turn_options='{}')`, alias)
}

func queueHoldCase(canSteer bool, mode deliverypolicy.Mode) string {
	earliest := `(SELECT MIN(m.id) FROM message_queue m WHERE m.session_id=q.session_id AND m.status IN ('pending','inflight')%s)`
	steer := ""
	if canSteer {
		steer = `
		WHEN ` + queueTargetStatus + `='busy' AND ` + queueSteerRow("q", mode) + ` THEN CASE WHEN q.id!=` + fmt.Sprintf(earliest, " AND "+queueSteerRow("m", mode)) + ` THEN 'behind' WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused' WHEN q.available_at_ms>?1 THEN 'retrying' ELSE '' END`
	}
	return `CASE WHEN q.status!='pending' THEN ''
		WHEN q.host_request!='' THEN 'hostUnreachable'
		WHEN ` + fmt.Sprintf(limitHeldFilter, "q.session_id") + ` THEN 'limited'
		WHEN ` + fmt.Sprintf(queueToPeerSocket, "q") + ` AND ` + fmt.Sprintf(queuePeerRow, "q") + ` THEN CASE WHEN q.id!=` + fmt.Sprintf(earliest, "") + ` THEN 'behind' WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused' WHEN q.available_at_ms>?1 THEN 'retrying' ELSE '' END
		WHEN COALESCE(sr.waiting_id,'')!='' THEN 'waiting'` + steer + `
		WHEN ` + queueTargetStatus + `='busy' THEN 'busy'
		WHEN ` + queueTargetStatus + `='offline' THEN 'offline'
		WHEN ` + queueTargetStatus + ` NOT IN ('idle','notLoaded') THEN 'unknown'
		WHEN ` + queueTargetStatus + `='notLoaded' AND NOT (COALESCE(s.surface,'')='codex' AND COALESCE(s.transport,'')='desktop') THEN 'notLoaded'
		WHEN q.id!=` + fmt.Sprintf(earliest, "") + ` THEN 'behind'
		WHEN q.refused=1 AND q.available_at_ms>?1 THEN 'refused'
		WHEN q.available_at_ms>?1 THEN 'retrying'
		ELSE '' END`
}

func queueRowSelect(mode deliverypolicy.Mode) string {
	return `SELECT q.id,q.session_id,q.message,q.model,q.source_session_id,q.turn_options,q.status,q.attempts,q.last_error,q.queued_at,q.dead_at_ms,q.evidence,q.operation,q.busy_delivery,
	` + queueHoldCase(true, mode) + `,
	` + queueHoldCase(false, mode) + `,
	COALESCE(s.surface,''),COALESCE(s.transport,''),COALESCE(s.bridge,''),COALESCE(s.runtime_kind,'')
	FROM message_queue q LEFT JOIN sessions s ON s.id=q.session_id LEFT JOIN session_runtime sr ON sr.session_id=q.session_id`
}

func scanQueueRow(scanner rowScanner, adapters surface.Adapters, now time.Time) (QueueRow, error) {
	var row QueueRow
	var evidence, targetSurface, transport, bridge, runtimeKind string
	var steerHold, otherHold QueueHold
	if err := scanner.Scan(&row.ID, &row.SessionID, &row.Message, &row.Model, &row.SourceSessionID, &row.TurnOptions, &row.Status, &row.Attempts, &row.LastError, &row.QueuedAt, &row.DeadAt, &evidence, &row.Operation, &row.BusyDelivery, &steerHold, &otherHold, &targetSurface, &transport, &bridge, &runtimeKind); err != nil {
		return QueueRow{}, err
	}
	target := &surface.Session{ID: row.SessionID, Surface: surface.SurfaceKind(targetSurface), Transport: transport, Bridge: bridge, RuntimeKind: surface.RuntimeKind(runtimeKind)}
	row.HeldBy = otherHold
	if adapter, err := adapters.For(target); err == nil && surface.EffectiveCapabilities(adapter, target).Steer {
		row.HeldBy = steerHold
	}
	row.Historical = queueRowIsHistorical(row, now)
	row.Evidence = queueEvidence(row, surface.DeliveryEvidence(evidence))
	return row, nil
}

func (r *Registry) ListQueue(includeDelivered bool, adapters surface.Adapters) ([]QueueRow, error) {
	now := r.now()
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	query := queueRowSelect(mode)
	args := []any{now.UnixMilli()}
	if !includeDelivered {
		query += ` WHERE q.status NOT IN ('delivered','canceled') AND (q.status!='dead' OR q.dead_at_ms>?)`
		args = append(args, now.Add(-deadReviewWindow).UnixMilli())
	}
	query += ` ORDER BY q.id`
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]QueueRow, 0), rows, func(row rowScanner) (QueueRow, error) {
		return scanQueueRow(row, adapters, now)
	})
}

func (r *Registry) QueueItem(id int64, adapters surface.Adapters) (*QueueRow, error) {
	now := r.now()
	mode, err := r.busyMode()
	if err != nil {
		return nil, err
	}
	row, err := scanQueueRow(r.read.QueryRow(queueRowSelect(mode)+` WHERE q.id=?`, now.UnixMilli(), id), adapters, now)
	if err != nil {
		return nil, err
	}
	return &row, nil
}

func queueRowIsHistorical(row QueueRow, now time.Time) bool {
	if row.Status == "delivered" || row.Status == "canceled" {
		return true
	}
	return row.Status == "dead" && row.DeadAt <= now.Add(-deadReviewWindow).UnixMilli()
}

func queueEvidence(row QueueRow, recorded surface.DeliveryEvidence) surface.DeliveryEvidence {
	if recorded != "" {
		return recorded
	}
	if row.Status == "dead" && strings.HasPrefix(row.LastError, "delivery outcome is unknown") {
		return surface.EvidenceUnknown
	}
	return QueueStatusEvidence(row.Status)
}

func QueueStatusEvidence(status string) surface.DeliveryEvidence {
	switch status {
	case "pending", "inflight":
		return surface.EvidenceQueued
	case "delivered":
		return surface.EvidenceDelivered
	case "canceled":
		return surface.EvidenceCanceled
	case "dead":
		return surface.EvidenceFailed
	default:
		return ""
	}
}

func (r *Registry) ListAttentionItems(includeResolved bool) ([]AttentionItem, error) {
	query := `SELECT id,session_id,queue_id,reason,requested_action,created_at,resolved_at,resolution FROM attention_items`
	if !includeResolved {
		query += ` WHERE resolved_at=''`
	}
	query += ` ORDER BY created_at DESC,id DESC`
	rows, err := r.read.Query(query)
	if err != nil {
		return nil, err
	}
	return appendRows(make([]AttentionItem, 0), rows, func(row rowScanner) (AttentionItem, error) {
		var item AttentionItem
		err := row.Scan(&item.ID, &item.SessionID, &item.QueueID, &item.Reason, &item.RequestedAction, &item.CreatedAt, &item.ResolvedAt, &item.Resolution)
		return item, err
	})
}

const queueRefusedRow = `refused=1 AND status IN ('pending','inflight')`

func (r *Registry) ReconcileAttentionItems(now time.Time) error {
	if open, err := r.readExists(`SELECT EXISTS(SELECT 1 FROM message_queue WHERE status='dead' OR ` + queueRefusedRow + `) OR EXISTS(SELECT 1 FROM attention_items WHERE resolved_at='')`); err != nil || !open {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action)
		SELECT session_id,id,
			CASE
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 404%' THEN 'Delivery target is unavailable'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 401%' THEN 'Claude needs to reconnect'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 403%' THEN 'Claude denied this delivery'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 400%' THEN 'Delivery request needs correction'
				WHEN last_error LIKE 'delivery outcome is unknown%' THEN 'Delivery outcome could not be confirmed'
				WHEN last_error LIKE 'delivery rejected [target missing]:%' THEN 'Delivery target is unavailable'
				WHEN last_error LIKE 'delivery rejected [authentication needed]:%' THEN 'Claude needs to reconnect'
				WHEN last_error LIKE 'delivery rejected [access denied]:%' THEN 'Claude denied this delivery'
				WHEN last_error LIKE 'delivery rejected [invalid request]:%' THEN 'Delivery request needs correction'
				ELSE 'Delivery failed and needs a decision'
			END,
			CASE
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 404%' THEN 'Cancel and send this message to an available session'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 401%' THEN 'Reconnect Claude, then retry or cancel this message'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 403%' THEN 'Check Claude access, then retry or cancel this message'
				WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 400%' THEN 'Correct this message, then retry or cancel it'
				WHEN last_error LIKE 'delivery rejected [target missing]:%' THEN 'Cancel and send this message to an available session'
				WHEN last_error LIKE 'delivery rejected [authentication needed]:%' THEN 'Reconnect Claude, then retry or cancel this message'
				WHEN last_error LIKE 'delivery rejected [access denied]:%' THEN 'Check Claude access, then retry or cancel this message'
				WHEN last_error LIKE 'delivery rejected [invalid request]:%' THEN 'Correct this message, then retry or cancel it'
				ELSE 'Retry or cancel this message'
			END
		FROM message_queue WHERE status='dead' AND dead_at_ms>?
		ON CONFLICT(queue_id) DO UPDATE SET
			reason=excluded.reason,
			requested_action=excluded.requested_action
		WHERE attention_items.resolved_at=''`, now.Add(-deadReviewWindow).UnixMilli())
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action)
		SELECT session_id,id,'The target refused to start','Fix what the target reported; the message is delivered on its next attempt, or cancel it'
		FROM message_queue WHERE ` + queueRefusedRow + `
		ON CONFLICT(queue_id) DO UPDATE SET
			reason=excluded.reason,
			requested_action=excluded.requested_action,
			created_at=CASE WHEN attention_items.resolved_at!='' THEN datetime('now') ELSE attention_items.created_at END,
			resolved_at='',
			resolution=''`)
	if err != nil {
		return err
	}
	_, err = tx.Exec(`UPDATE attention_items SET
		resolved_at=datetime('now'),
		resolution=COALESCE((SELECT CASE WHEN status='dead' AND dead_at_ms<=? THEN 'history' WHEN status='pending' THEN 'retrying' WHEN status='delivered' THEN 'delivered' WHEN status='canceled' THEN 'canceled' ELSE status END FROM message_queue WHERE id=attention_items.queue_id),'removed')
		WHERE resolved_at='' AND NOT EXISTS (SELECT 1 FROM message_queue WHERE id=attention_items.queue_id AND (status='dead' AND dead_at_ms>? OR `+queueRefusedRow+`))`, now.Add(-deadReviewWindow).UnixMilli(), now.Add(-deadReviewWindow).UnixMilli())
	if err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return nil
}

func (r *Registry) RetryMessage(id int64) error {
	var sessionID, message, hostRequest string
	_ = r.db.QueryRow(`SELECT session_id,message,host_request FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message, &hostRequest)
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='pending',attempts=0,deferrals=0,last_error='',available_at_ms=0,inflight_at_ms=0,dead_at_ms=0,delivered=0,updated_at=datetime('now') WHERE id=? AND status='dead'`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not dead-lettered", id)
	}
	if err := retryQueuedDeliveryIntent(tx, id); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if hostRequest != "" {
		r.signalChange(ChangeHostDeliveries)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "retry", SessionID: sessionID, QueueID: id, Message: message, Result: "scheduled"})
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) CancelMessage(id int64) error {
	var sessionID, message string
	_ = r.db.QueryRow(`SELECT session_id,message FROM message_queue WHERE id=?`, id).Scan(&sessionID, &message)
	res, err := r.db.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE id=? AND status IN ('pending','dead')`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not pending or dead-lettered", id)
	}
	_ = r.RecordHistory(HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "removed from delivery queue"})
	return r.ReconcileAttentionItems(r.now())
}

func (r *Registry) CancelMessagesForSession(sessionID string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT id,message FROM message_queue WHERE session_id=? AND status='pending'`, sessionID)
	if err != nil {
		return 0, err
	}
	pending, err := appendRows(nil, rows, func(row rowScanner) (HistoryEntry, error) {
		var id int64
		var message string
		err := row.Scan(&id, &message)
		return HistoryEntry{Kind: "canceled", SessionID: sessionID, QueueID: id, Message: message, Result: "removed from pending queue"}, err
	})
	if err != nil {
		return 0, err
	}
	res, err := tx.Exec(`UPDATE message_queue SET refused=0,status='canceled',updated_at=datetime('now') WHERE session_id=? AND status='pending'`, sessionID)
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	for _, entry := range pending {
		_ = r.RecordHistory(entry)
	}
	count, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := r.ReconcileAttentionItems(r.now()); err != nil {
		return count, err
	}
	return count, nil
}

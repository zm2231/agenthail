package registry

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	HandoffTurnFinished  = "turn_finished"
	HandoffGoalCompleted = "goal_completed"
)

var relayDeliveryToken = regexp.MustCompile(`(?m)^\[agenthail delivery=([a-f0-9-]{36})\]\n`)

type RelayEvent struct {
	CompletedAt     time.Time
	ID              int64
	SourceSessionID string
	CompletionID    string
	InputID         string
	Message         string
	RelayHops       int
}

// PrepareRelaySource establishes the durable starting position before accepting
// a user-created route. Provider history preceding this position is not replayed.
func (r *Registry) PrepareRelaySource(ctx context.Context, adapter surface.Surface, session *surface.Session, handoff string) error {
	if handoff != "" && handoff != HandoffTurnFinished && handoff != HandoffGoalCompleted {
		return fmt.Errorf("handoff must be turn_finished or goal_completed")
	}
	if handoff == HandoffGoalCompleted && !surface.EffectiveCapabilities(session, adapter.Capabilities()).Goal {
		return fmt.Errorf("source does not support goal-completed handoffs")
	}
	reader, ok := adapter.(surface.CompletionReader)
	if !ok {
		return fmt.Errorf("source does not support replayable completions")
	}
	cursor, err := r.RelaySourceCursor(session.ID)
	if err != nil {
		return err
	}
	batch, err := reader.ReadCompletions(ctx, session, cursor)
	if err != nil {
		return err
	}
	return r.CaptureRelayCompletions(session.ID, cursor, batch)
}

func (r *Registry) RelaySourceCursor(sessionID string) (string, error) {
	var cursor string
	err := r.db.QueryRow(`SELECT cursor FROM relay_sources WHERE session_id=?`, sessionID).Scan(&cursor)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return cursor, err
}

func (r *Registry) CaptureRelayCompletions(sessionID, previous string, batch surface.CompletionBatch) error {
	if batch.Cursor == "" {
		return fmt.Errorf("completion reader returned an empty cursor")
	}
	if batch.Cursor == previous && len(batch.Events) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO relay_sources(session_id,cursor) VALUES(?,'') ON CONFLICT DO NOTHING`, sessionID); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE relay_sources SET cursor=? WHERE session_id=? AND cursor=?`, batch.Cursor, sessionID, previous)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return fmt.Errorf("relay source cursor changed concurrently; retry capture")
	}
	for _, event := range batch.Events {
		if event.ID == "" {
			return fmt.Errorf("completion event has no identity")
		}
		hops, err := relayCompletionHops(tx, sessionID, event)
		if err != nil {
			return err
		}
		completedAt := int64(0)
		if !event.CompletedAt.IsZero() {
			completedAt = event.CompletedAt.UnixMilli()
		}
		if _, err := tx.Exec(`INSERT INTO relay_events(source_session_id,completion_id,input_id,message,relay_hops,completed_at_ms) VALUES(?,?,?,?,?,?) ON CONFLICT(source_session_id,completion_id) DO NOTHING`, sessionID, event.ID, event.InputID, event.Text, hops, completedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func relayCompletionHops(tx *sql.Tx, sessionID string, event surface.Completion) (int, error) {
	match := relayDeliveryToken.FindStringSubmatch(event.User)
	if len(match) != 2 {
		return 0, nil
	}
	var hops int
	var inputID, completionID string
	err := tx.QueryRow(`SELECT q.relay_hops,p.input_id,p.completion_id FROM relay_provenance p JOIN message_queue q ON q.id=p.queue_id WHERE p.token=? AND q.session_id=? AND q.relay_hops>0`, match[1], sessionID).Scan(&hops, &inputID, &completionID)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if completionID != "" {
		if completionID == event.ID || (event.InputID != "" && inputID == event.InputID) {
			return hops, nil
		}
		return 0, nil
	}
	if _, err := tx.Exec(`UPDATE relay_provenance SET input_id=?,completion_id=? WHERE token=?`, event.InputID, event.ID, match[1]); err != nil {
		return 0, err
	}
	return hops, nil
}

func (r *Registry) PendingRelayEvents(route RouteRow) ([]RelayEvent, error) {
	rows, err := r.db.Query(`SELECT id,source_session_id,completion_id,input_id,message,relay_hops,completed_at_ms FROM relay_events WHERE source_session_id=? AND id>? ORDER BY id`, route.FromSession, route.EventCursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []RelayEvent
	for rows.Next() {
		var event RelayEvent
		var completedAt int64
		if err := rows.Scan(&event.ID, &event.SourceSessionID, &event.CompletionID, &event.InputID, &event.Message, &event.RelayHops, &completedAt); err != nil {
			return nil, err
		}
		if completedAt != 0 {
			event.CompletedAt = time.UnixMilli(completedAt)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *Registry) SetRelayError(routeID int64, message string) error {
	_, err := r.db.Exec(`UPDATE routes SET last_error=? WHERE id=? AND last_error!=?`, message, routeID, message)
	return err
}

// ConsumeRelay commits route progress, the queue item, delivery intent, one-shot
// deactivation and receipt together. An empty payload explicitly skips an event.
func (r *Registry) ConsumeRelay(route RouteRow, event RelayEvent, payload, busyDelivery, goalKey string) (int64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE routes SET event_cursor=?,last_goal_key=?,last_error='',active=CASE WHEN once_only=1 AND ?!='' THEN 0 ELSE active END WHERE id=? AND active=1 AND event_cursor=? AND from_session=? AND EXISTS(SELECT 1 FROM relay_events WHERE id=? AND source_session_id=routes.from_session)`, event.ID, goalKey, payload, route.ID, route.EventCursor, event.SourceSessionID, event.ID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	if n != 1 || event.ID <= route.EventCursor {
		return 0, fmt.Errorf("relay route changed concurrently; retry consumption")
	}
	var queueID int64
	if payload != "" {
		token := uuid.NewString()
		payload = fmt.Sprintf("[agenthail delivery=%s]\n%s", token, payload)
		key := fmt.Sprintf("relay:%d:%s", route.ID, event.CompletionID)
		queueID, _, err = enqueueMessageTx(tx, route.ToSession, payload, key, surface.SendOptions{SourceSessionID: route.FromSession, BusyDelivery: busyDelivery}, event.RelayHops+1, QueueOperationMessage, true)
		if err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO relay_provenance(token,queue_id) SELECT ?,id FROM message_queue WHERE id=? AND message=? ON CONFLICT(queue_id) DO NOTHING`, token, queueID, payload); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO relay_deliveries(route_id,completion_id) VALUES(?,?) ON CONFLICT DO NOTHING`, route.ID, event.CompletionID); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT INTO delivery_history(kind,session_id,source_session_id,route_id,queue_id,completion_id,message,result) VALUES('relay',?,?,?,?,?,?,'queued')`, route.ToSession, route.FromSession, route.ID, queueID, event.CompletionID, event.Message); err != nil {
			return 0, err
		}
	}
	return queueID, tx.Commit()
}

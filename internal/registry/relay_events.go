package registry

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	HandoffTurnFinished  = "turn_finished"
	HandoffGoalCompleted = "goal_completed"
)

var relayEnvelope = regexp.MustCompile(`(?m)^\[agenthail relay hops=\d+ id=(\d+) turn=([^\]\s]+) source=`)

// relaySourceKey names the conversation a session row reads completions from.
// A Claude row reads its transcript; a Claude row without one has no readable
// source.
const relaySourceKey = `CASE WHEN s.surface='claude' THEN CASE WHEN s.transcript='' THEN NULL ELSE 'claude:'||s.transcript END ELSE s.id END`

var errNoRelayTranscript = errors.New("relay source has no transcript yet")

// ErrRelayCursorMoved means another capture of the same conversation committed
// first. Its batch may end before this one, so the caller must read again from
// the new cursor to cover what it observed.
var ErrRelayCursorMoved = errors.New("relay source cursor moved; read again")

func relaySourceKeyOf(q catalogQuerier, sessionID string) (string, error) {
	var key sql.NullString
	if err := q.QueryRow(`SELECT `+relaySourceKey+` FROM sessions s WHERE s.id=?`, sessionID).Scan(&key); err != nil {
		if err == sql.ErrNoRows {
			return "", fmt.Errorf("relay source %s is not registered", sessionID)
		}
		return "", err
	}
	if !key.Valid {
		return "", errNoRelayTranscript
	}
	return key.String, nil
}

type RelayEvent struct {
	CompletedAt  time.Time
	ID           int64
	SourceKey    string
	CompletionID string
	InputID      string
	Message      string
	RelayHops    int
}

// PrepareRelaySource establishes the durable starting position before accepting
// a user-created route. Provider history preceding this position is not replayed.
func (r *Registry) PrepareRelaySource(ctx context.Context, adapter surface.Surface, session *surface.Session, handoff string) error {
	if handoff != "" && handoff != HandoffTurnFinished && handoff != HandoffGoalCompleted {
		return fmt.Errorf("handoff must be turn_finished or goal_completed")
	}
	if handoff == HandoffGoalCompleted && !surface.EffectiveCapabilities(adapter, session).Goal {
		return fmt.Errorf("source does not support goal-completed handoffs")
	}
	reader, ok := adapter.(surface.CompletionReader)
	if !ok {
		return fmt.Errorf("source does not support replayable completions")
	}
	for attempt := 0; ; attempt++ {
		cursor, err := r.RelaySourceCursor(session.ID)
		if err != nil {
			return err
		}
		batch, err := reader.ReadCompletions(ctx, session, cursor)
		if err != nil {
			return err
		}
		err = r.CaptureRelayCompletions(session.ID, cursor, batch)
		if !errors.Is(err, ErrRelayCursorMoved) || attempt == 4 {
			return err
		}
	}
}

func (r *Registry) RelaySourceCursor(sessionID string) (string, error) {
	key, err := relaySourceKeyOf(r.read, sessionID)
	if err != nil {
		return "", err
	}
	var cursor string
	err = r.read.QueryRow(`SELECT cursor FROM relay_sources WHERE source_key=?`, key).Scan(&cursor)
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
	key, err := relaySourceKeyOf(tx, sessionID)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO relay_sources(source_key,cursor) VALUES(?,'') ON CONFLICT DO NOTHING`, key); err != nil {
		return err
	}
	res, err := tx.Exec(`UPDATE relay_sources SET cursor=? WHERE source_key=? AND cursor=?`, batch.Cursor, key, previous)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		var current string
		if err := tx.QueryRow(`SELECT cursor FROM relay_sources WHERE source_key=?`, key).Scan(&current); err != nil {
			return err
		}
		if current == batch.Cursor {
			return nil
		}
		return ErrRelayCursorMoved
	}
	for _, event := range batch.Events {
		if event.ID == "" {
			return fmt.Errorf("completion event has no identity")
		}
		hops, err := relayCompletionHops(tx, key, event)
		if err != nil {
			return err
		}
		completedAt := int64(0)
		if !event.CompletedAt.IsZero() {
			completedAt = event.CompletedAt.UnixMilli()
		}
		if _, err := tx.Exec(`INSERT INTO relay_events(source_key,completion_id,input_id,message,relay_hops,completed_at_ms) VALUES(?,?,?,?,?,?) ON CONFLICT(source_key,completion_id) DO NOTHING`, key, event.ID, event.InputID, event.Text, hops, completedAt); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// relayCompletionHops inherits hops only for the first input that carried a
// queued relay envelope into this conversation, through any of its rows. The
// envelope names the route and source completion ahead of the free-text source
// label; together they identify the queue row by its delivery key.
func relayCompletionHops(tx *sql.Tx, key string, event surface.Completion) (int, error) {
	match := relayEnvelope.FindStringSubmatch(event.User)
	if len(match) != 3 {
		return 0, nil
	}
	var queueID int64
	var hops int
	err := tx.QueryRow(`SELECT id,relay_hops FROM message_queue WHERE delivery_key=? AND relay_hops>0 AND session_id IN (SELECT s.id FROM sessions s WHERE `+relaySourceKey+`=?)`, "relay:"+match[1]+":"+match[2], key).Scan(&queueID, &hops)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if _, err := tx.Exec(`INSERT INTO relay_provenance(queue_id,input_id,completion_id) VALUES(?,?,?) ON CONFLICT(queue_id) DO NOTHING`, queueID, event.InputID, event.ID); err != nil {
		return 0, err
	}
	var inputID, completionID string
	if err := tx.QueryRow(`SELECT input_id,completion_id FROM relay_provenance WHERE queue_id=?`, queueID).Scan(&inputID, &completionID); err != nil {
		return 0, err
	}
	if completionID == event.ID || (event.InputID != "" && inputID == event.InputID) {
		return hops, nil
	}
	return 0, nil
}

func (r *Registry) PendingRelayEvents(route RouteRow) ([]RelayEvent, error) {
	key, err := relaySourceKeyOf(r.read, route.FromSession)
	if err != nil {
		return nil, err
	}
	rows, err := r.read.Query(`SELECT id,source_key,completion_id,input_id,message,relay_hops,completed_at_ms FROM relay_events WHERE source_key=? AND id>? ORDER BY id`, key, route.EventCursor)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []RelayEvent
	for rows.Next() {
		var event RelayEvent
		var completedAt int64
		if err := rows.Scan(&event.ID, &event.SourceKey, &event.CompletionID, &event.InputID, &event.Message, &event.RelayHops, &completedAt); err != nil {
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
	// A destination a paired host owns gets the relay as a held send to that
	// host, written in the same transaction that advances the cursor.
	var hostSend []byte
	if payload != "" {
		owner, err := r.SessionHost(route.ToSession)
		if err != nil {
			return 0, err
		}
		if owner != "" {
			if hostSend, err = r.hostSendRequest(HostSend{SessionID: route.ToSession, Message: payload, SourceSessionID: route.FromSession, BusyDelivery: busyDelivery, RelayHops: event.RelayHops + 1}); err != nil {
				return 0, err
			}
		}
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE routes SET event_cursor=?,last_goal_key=?,last_error='',active=CASE WHEN once_only=1 AND ?!='' THEN 0 ELSE active END WHERE id=? AND active=1 AND event_cursor=? AND from_session=? AND EXISTS(SELECT 1 FROM relay_events e JOIN sessions s ON s.id=routes.from_session WHERE e.id=? AND e.source_key=`+relaySourceKey+`)`, event.ID, goalKey, payload, route.ID, route.EventCursor, route.FromSession, event.ID)
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
		key := fmt.Sprintf("relay:%d:%s", route.ID, event.CompletionID)
		if hostSend != nil {
			queueID, err = queueHostDeliveryTx(tx, r.now(), route.ToSession, payload, key, hostSend, false)
		} else {
			queueID, _, err = enqueueMessageTx(tx, r.now(), route.ToSession, payload, key, surface.SendOptions{SourceSessionID: route.FromSession, BusyDelivery: busyDelivery}, event.RelayHops+1, QueueOperationMessage, true)
		}
		if err != nil {
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

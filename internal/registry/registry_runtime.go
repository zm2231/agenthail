package registry

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type RuntimeState struct {
	LastStatus           surface.SessionStatus
	ActiveTurnID         string
	CompletedTurnID      string
	RelayHops            int
	NotificationArmed    bool
	HeldCompletionTurnID string
	UpdatedAt            time.Time
}

func (r *Registry) RuntimeState(sessionID string) (RuntimeState, bool, error) {
	var state RuntimeState
	var status, updatedAt string
	var notificationArmed int
	err := r.read.QueryRow(`SELECT last_status,active_turn_id,completed_turn_id,relay_hops,notification_armed,held_completion_turn_id,updated_at FROM session_runtime WHERE session_id=? AND turn_observed=1`, sessionID).Scan(&status, &state.ActiveTurnID, &state.CompletedTurnID, &state.RelayHops, &notificationArmed, &state.HeldCompletionTurnID, &updatedAt)
	if err == sql.ErrNoRows {
		return state, false, nil
	}
	if err != nil {
		return state, false, err
	}
	state.LastStatus = surface.SessionStatus(status)
	state.NotificationArmed = notificationArmed != 0
	if updatedAt != "" {
		state.UpdatedAt, err = parseSQLiteTimestamp(updatedAt)
		if err != nil {
			return state, false, fmt.Errorf("parse runtime state timestamp: %w", err)
		}
	}
	return state, true, nil
}

func (r *Registry) MarkDeliveryStarted(sessionID, activeTurnID, completedTurnID string) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,notification_armed,turn_observed,updated_at) VALUES(?,?,?,?,1,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=CASE WHEN session_runtime.turn_observed=1 THEN session_runtime.completed_turn_id ELSE excluded.completed_turn_id END,notification_armed=1,turn_observed=1,updated_at=datetime('now')`, sessionID, string(surface.StatusBusy), activeTurnID, completedTurnID)
	return err
}

func (r *Registry) SaveRuntimeState(sessionID string, observation surface.TurnObservation) error {
	_, err := r.db.Exec(`INSERT INTO session_runtime(session_id,last_status,active_turn_id,completed_turn_id,turn_observed,updated_at) VALUES(?,?,?,?,1,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET last_status=excluded.last_status,active_turn_id=excluded.active_turn_id,completed_turn_id=excluded.completed_turn_id,turn_observed=1,relay_hops=CASE WHEN excluded.completed_turn_id != session_runtime.completed_turn_id THEN 0 ELSE session_runtime.relay_hops END,updated_at=datetime('now')`, sessionID, string(observation.Status), observation.ActiveTurnID, observation.CompletedTurnID)
	return err
}

func (r *Registry) HoldCompletion(sessionID, turnID string) error {
	_, err := r.db.Exec(`UPDATE session_runtime SET held_completion_turn_id=? WHERE session_id=?`, turnID, sessionID)
	return err
}

func (r *Registry) ClearCompletionNotification(sessionID string) error {
	_, err := r.db.Exec(`UPDATE session_runtime SET notification_armed=0 WHERE session_id=?`, sessionID)
	return err
}

func (r *Registry) RecordRelayDelivery(routeID int64, completionID string) (bool, error) {
	res, err := r.db.Exec(`INSERT OR IGNORE INTO relay_deliveries(route_id,completion_id) VALUES(?,?)`, routeID, completionID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n == 1, err
}

func (r *Registry) ForgetRelayDelivery(routeID int64, completionID string) error {
	_, err := r.db.Exec(`DELETE FROM relay_deliveries WHERE route_id=? AND completion_id=?`, routeID, completionID)
	return err
}

// TurnNotice is the completion this host last notified; viewing hosts
// notify from it rather than judging completion themselves.
type TurnNotice struct {
	TurnID    string
	Outcome   string
	RequestID string
	At        time.Time
}

func (r *Registry) TurnNotices() (map[string]TurnNotice, error) {
	rows, err := r.read.Query(`SELECT session_id,notice_turn_id,notice_outcome,notice_request_id,notice_at_ms FROM session_runtime WHERE notice_turn_id<>''`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	notices := map[string]TurnNotice{}
	for rows.Next() {
		var sessionID string
		var notice TurnNotice
		var atMS int64
		if err := rows.Scan(&sessionID, &notice.TurnID, &notice.Outcome, &notice.RequestID, &atMS); err != nil {
			return nil, err
		}
		notice.At = time.UnixMilli(atMS).UTC()
		notices[sessionID] = notice
	}
	return notices, rows.Err()
}

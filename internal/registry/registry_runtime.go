package registry

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

type RuntimeState struct {
	LastStatus        surface.SessionStatus
	ActiveTurnID      string
	CompletedTurnID   string
	RelayHops         int
	NotificationArmed bool
	UpdatedAt         time.Time
}

func (r *Registry) RuntimeState(sessionID string) (RuntimeState, bool, error) {
	var state RuntimeState
	var status, updatedAt string
	var notificationArmed int
	err := r.read.QueryRow(`SELECT last_status,active_turn_id,completed_turn_id,relay_hops,notification_armed,updated_at FROM session_runtime WHERE session_id=? AND turn_observed=1`, sessionID).Scan(&status, &state.ActiveTurnID, &state.CompletedTurnID, &state.RelayHops, &notificationArmed, &updatedAt)
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

func (r *Registry) SessionWaiting(sessionID string) (*surface.Waiting, error) {
	waiting, _, err := r.WaitingState(sessionID)
	return waiting, err
}

func (r *Registry) WaitingState(sessionID string) (*surface.Waiting, bool, error) {
	return scanWaiting(r.read.QueryRow(`SELECT waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable FROM session_runtime WHERE session_id=?`, sessionID))
}

func scanWaiting(row *sql.Row) (*surface.Waiting, bool, error) {
	var kind, id string
	var sinceMS int64
	var announced, answerable bool
	err := row.Scan(&kind, &id, &sinceMS, &announced, &answerable)
	if err == sql.ErrNoRows || err == nil && id == "" {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return &surface.Waiting{Kind: surface.WaitingKind(kind), ID: id, Since: time.UnixMilli(sinceMS).UTC(), Answerable: answerable}, announced, nil
}

type WaitingRecord struct {
	Waiting           *surface.Waiting
	Announce          bool
	Changed           bool
	Replaced          *surface.Waiting
	ReplacedAnnounced bool
}

func (r *Registry) RecordWaiting(sessionID string, waiting *surface.Waiting, now time.Time) (WaitingRecord, error) {
	current, announced, err := r.WaitingState(sessionID)
	if err != nil {
		return WaitingRecord{}, err
	}
	if surface.SameWaiting(current, waiting) {
		return WaitingRecord{Waiting: current, Announce: current != nil && !announced}, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return WaitingRecord{}, err
	}
	defer tx.Rollback()
	stored, announced, err := scanWaiting(tx.QueryRow(`SELECT waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable FROM session_runtime WHERE session_id=?`, sessionID))
	if err != nil {
		return WaitingRecord{}, err
	}
	if surface.SameWaiting(stored, waiting) {
		return WaitingRecord{Waiting: stored, Announce: stored != nil && !announced}, tx.Commit()
	}
	if waiting == nil {
		if _, err := tx.Exec(`UPDATE session_runtime SET waiting_kind='',waiting_id='',waiting_since_ms=0,waiting_announced=0,waiting_answerable=0 WHERE session_id=?`, sessionID); err != nil {
			return WaitingRecord{}, err
		}
		return WaitingRecord{Changed: true, Replaced: stored, ReplacedAnnounced: announced}, tx.Commit()
	}
	next := *waiting
	if next.Since.IsZero() {
		next.Since = now.UTC()
	}
	if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,waiting_kind,waiting_id,waiting_since_ms,waiting_announced,waiting_answerable,updated_at) SELECT ?,?,?,?,0,?,datetime('now') WHERE EXISTS(SELECT 1 FROM sessions WHERE id=?)
		ON CONFLICT(session_id) DO UPDATE SET waiting_kind=excluded.waiting_kind,waiting_id=excluded.waiting_id,waiting_since_ms=excluded.waiting_since_ms,waiting_announced=0,waiting_answerable=excluded.waiting_answerable`, sessionID, string(next.Kind), next.ID, next.Since.UnixMilli(), next.Answerable, sessionID); err != nil {
		return WaitingRecord{}, err
	}
	return WaitingRecord{Waiting: &next, Announce: true, Changed: true, Replaced: stored, ReplacedAnnounced: announced}, tx.Commit()
}

func (r *Registry) MarkWaitingAnnounced(sessionID, id string) error {
	_, err := r.db.Exec(`UPDATE session_runtime SET waiting_announced=1 WHERE session_id=? AND waiting_id=?`, sessionID, id)
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

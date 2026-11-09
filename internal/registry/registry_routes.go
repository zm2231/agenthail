package registry

import (
	"errors"
	"fmt"
	"regexp"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) AddRoute(from, to, pattern string) (int64, error) {
	return r.AddRouteWithOptions(from, to, pattern, false)
}

func (r *Registry) AddRouteWithOptions(from, to, pattern string, once bool) (int64, error) {
	return r.AddRelayRoute(from, to, pattern, once, HandoffTurnFinished)
}

func (r *Registry) AddRelayRoute(from, to, pattern string, once bool, handoff string) (int64, error) {
	if handoff == "" {
		handoff = HandoffTurnFinished
	}
	if handoff != HandoffTurnFinished && handoff != HandoffGoalCompleted {
		return 0, fmt.Errorf("handoff must be turn_finished or goal_completed")
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return 0, fmt.Errorf("invalid relay pattern: %w", err)
	}
	if from == to {
		return 0, fmt.Errorf("relay route cannot target its source")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	var moving bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_moves WHERE conversation=? AND role='out' AND state IN ('preparing','sent'))`, from).Scan(&moving); err != nil {
		return 0, err
	}
	if moving {
		return 0, fmt.Errorf("relay source %s is moving to another host; add the relay once the move has finished", from)
	}
	var cycle int
	err = tx.QueryRow(`
		WITH RECURSIVE reachable(id) AS (
			SELECT to_session FROM routes WHERE from_session=? AND active=1
			UNION
			SELECT routes.to_session FROM routes JOIN reachable ON routes.from_session=reachable.id WHERE routes.active=1
		)
		SELECT COUNT(*) FROM reachable WHERE id=?`, to, from).Scan(&cycle)
	if err != nil {
		return 0, err
	}
	if cycle > 0 {
		return 0, fmt.Errorf("relay route would create a cycle")
	}
	res, err := tx.Exec(`INSERT INTO routes (from_session,to_session,pattern,once_only,handoff,event_cursor) VALUES (?,?,?,?,?,COALESCE((SELECT MAX(id) FROM relay_events),0))`, from, to, pattern, b2i(once), handoff)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return id, nil
}

func (r *Registry) MoveRelayRoutes(from, to string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, query := range routeMoves {
		if _, err := tx.Exec(query, from, to); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type WatchedSession struct {
	ID      string
	Surface surface.SurfaceKind
}

func (r *Registry) WatchedSessions() ([]WatchedSession, error) {
	rows, err := r.read.Query(`
		SELECT DISTINCT s.id,s.surface
		FROM sessions s
		WHERE s.host_id='' AND s.id IN (
			SELECT from_session FROM routes WHERE active=1
			UNION SELECT to_session FROM routes WHERE active=1
			UNION SELECT session_id FROM message_queue WHERE status IN ('pending','inflight')
			UNION SELECT session_id FROM session_runtime WHERE held_completion_turn_id<>''
			UNION SELECT child_session_id FROM tasks WHERE state='running'
		)
		ORDER BY s.surface,s.id`)
	if err != nil {
		return nil, err
	}
	return appendRows(nil, rows, func(row rowScanner) (WatchedSession, error) {
		var session WatchedSession
		var kind string
		err := row.Scan(&session.ID, &kind)
		session.Surface = surface.SurfaceKind(kind)
		return session, err
	})
}

func (r *Registry) RemoveRoute(id int64) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var source string
	if err := tx.QueryRow(`SELECT from_session FROM routes WHERE id=?`, id).Scan(&source); err != nil {
		return fmt.Errorf("relay route %d does not exist: %w", id, err)
	}
	if _, err := tx.Exec(`DELETE FROM routes WHERE id=?`, id); err != nil {
		return err
	}
	key, err := relaySourceKeyOf(tx, source)
	if err != nil && !errors.Is(err, errNoRelayTranscript) {
		return err
	}
	if err == nil {
		if err := dropOrphanRelaySourceTx(tx, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

type RouteRow struct {
	Handoff      string `json:"handoff"`
	EventCursor  int64  `json:"eventCursor"`
	PendingCount int64  `json:"pendingCount"`
	LastGoalKey  string `json:"-"`
	LastError    string `json:"lastError,omitempty"`
	ID           int64  `json:"id"`
	FromSession  string `json:"fromSession"`
	ToSession    string `json:"toSession"`
	Pattern      string `json:"pattern"`
	Once         bool   `json:"once"`
	Active       bool   `json:"active"`
	FireCount    int64  `json:"fireCount"`
	LastFiredAt  string `json:"lastFiredAt,omitempty"`
}

func (r *Registry) ListRoutes() ([]RouteRow, error) {
	rows, err := r.read.Query(`SELECT r.id, r.from_session, r.to_session, r.pattern, r.once_only, r.active,
		COUNT(d.completion_id), COALESCE(MAX(d.delivered_at), ''), r.handoff, r.event_cursor, r.last_goal_key, r.last_error, (SELECT COUNT(*) FROM relay_events e JOIN sessions s ON s.id=r.from_session WHERE e.source_key=` + relaySourceKey + ` AND e.id>r.event_cursor)
		FROM routes r LEFT JOIN relay_deliveries d ON d.route_id = r.id
		GROUP BY r.id, r.from_session, r.to_session, r.pattern, r.once_only, r.active
		ORDER BY r.id`)
	if err != nil {
		return nil, err
	}
	return appendRows(nil, rows, func(row rowScanner) (RouteRow, error) {
		var route RouteRow
		var once, active int
		err := row.Scan(&route.ID, &route.FromSession, &route.ToSession, &route.Pattern, &once, &active, &route.FireCount, &route.LastFiredAt, &route.Handoff, &route.EventCursor, &route.LastGoalKey, &route.LastError, &route.PendingCount)
		route.Once = once != 0
		route.Active = active != 0
		return route, err
	})
}

func (r *Registry) DeactivateRoute(id int64) error {
	res, err := r.db.Exec(`UPDATE routes SET active=0 WHERE id=? AND active=1`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("active relay route %d does not exist", id)
	}
	return nil
}

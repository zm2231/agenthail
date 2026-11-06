package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type TurnOption struct {
	Label string `json:"label"`
	Does  string `json:"does,omitempty"`
}

type TurnLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type TurnAsk struct {
	Question  string       `json:"question"`
	Options   []TurnOption `json:"options"`
	Suggested *int         `json:"suggested,omitempty"`
	Links     []TurnLink   `json:"links,omitempty"`
}

type TurnEnd struct {
	SessionID       string
	StartedAt       time.Time
	EndedAt         time.Time
	TurnID          string
	CompletedTurnID string
	Needs           string
	Headline        string
	Asks            []TurnAsk
	Next            []string
	Source          string
	RoutedTo        string
}

var ErrTurnAlreadyEnded = errors.New("this turn has already ended")

const (
	TurnSourceTool   = "tool"
	TurnSourceNudged = "nudged"
)

// Only the newest turn without an end is kept, so a session that never ends its turns holds one row.
func (r *Registry) OpenTurn(sessionID string, at time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM turn_ends WHERE session_id=? AND ended_at_ms=0`, sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO turn_ends(session_id,started_at_ms) VALUES(?,MAX(?,COALESCE((SELECT MAX(started_at_ms)+1 FROM turn_ends WHERE session_id=?),0))) ON CONFLICT DO NOTHING`, sessionID, at.UnixMilli(), sessionID); err != nil {
		return err
	}
	return tx.Commit()
}

// A turn has started since the last end when one was opened, the last one completed, or the runtime names another active turn.
func (r *Registry) EndTurn(end TurnEnd, activeTurnID string, at time.Time) (TurnEnd, error) {
	asks, err := json.Marshal(end.Asks)
	if err != nil {
		return TurnEnd{}, err
	}
	next, err := json.Marshal(end.Next)
	if err != nil {
		return TurnEnd{}, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return TurnEnd{}, err
	}
	defer tx.Rollback()
	var started, ended int64
	var turnID, completed string
	err = tx.QueryRow(`SELECT started_at_ms,ended_at_ms,turn_id,completed_turn_id FROM turn_ends WHERE session_id=? ORDER BY started_at_ms DESC LIMIT 1`, end.SessionID).Scan(&started, &ended, &turnID, &completed)
	fresh := errors.Is(err, sql.ErrNoRows) || err == nil && (completed != "" || ended > 0 && turnID != "" && activeTurnID != "" && turnID != activeTurnID)
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return TurnEnd{}, err
	case fresh:
		started = max(at.UnixMilli(), started+1)
		if _, err := tx.Exec(`INSERT INTO turn_ends(session_id,started_at_ms) VALUES(?,?)`, end.SessionID, started); err != nil {
			return TurnEnd{}, err
		}
	case ended > 0:
		return TurnEnd{}, ErrTurnAlreadyEnded
	}
	if _, err := tx.Exec(`UPDATE turn_ends SET ended_at_ms=?,turn_id=?,needs=?,headline=?,asks_json=?,next_json=?,source=CASE WHEN source=? THEN source ELSE ? END,routed_to=? WHERE session_id=? AND started_at_ms=?`,
		at.UnixMilli(), activeTurnID, end.Needs, end.Headline, string(asks), string(next), TurnSourceNudged, end.Source, end.RoutedTo, end.SessionID, started); err != nil {
		return TurnEnd{}, err
	}
	if err := tx.QueryRow(`SELECT source FROM turn_ends WHERE session_id=? AND started_at_ms=?`, end.SessionID, started).Scan(&end.Source); err != nil {
		return TurnEnd{}, err
	}
	if err := tx.Commit(); err != nil {
		return TurnEnd{}, err
	}
	end.StartedAt, end.EndedAt, end.TurnID = time.UnixMilli(started).UTC(), time.UnixMilli(at.UnixMilli()).UTC(), activeTurnID
	return end, nil
}

func (r *Registry) TurnEnded(sessionID, turnID string) (bool, error) {
	var ended int64
	var rowTurn, completed string
	err := r.read.QueryRow(`SELECT ended_at_ms,turn_id,completed_turn_id FROM turn_ends WHERE session_id=? ORDER BY started_at_ms DESC LIMIT 1`, sessionID).Scan(&ended, &rowTurn, &completed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ended > 0 && completed == "" && (turnID == "" || rowTurn == "" || rowTurn == turnID), nil
}

func (r *Registry) MarkTurnNudged(sessionID string, at time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE turn_ends SET source=? WHERE session_id=? AND ended_at_ms=0 AND completed_turn_id='' AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=?)`, TurnSourceNudged, sessionID, sessionID)
	if err != nil {
		return err
	}
	if marked, err := result.RowsAffected(); err != nil {
		return err
	} else if marked == 0 {
		if _, err := tx.Exec(`INSERT INTO turn_ends(session_id,started_at_ms,source) VALUES(?,MAX(?,COALESCE((SELECT MAX(started_at_ms)+1 FROM turn_ends WHERE session_id=?),0)),?)`, sessionID, at.UnixMilli(), sessionID, TurnSourceNudged); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Registry) RouteTurnEnd(sessionID string, startedAt time.Time, to string) error {
	_, err := r.db.Exec(`UPDATE turn_ends SET routed_to=? WHERE session_id=? AND started_at_ms=?`, to, sessionID, startedAt.UnixMilli())
	return err
}

func (r *Registry) BindTurnCompletion(sessionID, completedTurnID string) error {
	_, err := r.db.Exec(`UPDATE turn_ends SET completed_turn_id=? WHERE session_id=? AND completed_turn_id='' AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=?)`, completedTurnID, sessionID, sessionID)
	return err
}

func (r *Registry) CompletedTurnEnd(sessionID, completedTurnID string) (TurnEnd, bool, error) {
	rows, err := r.read.Query(`SELECT `+turnEndColumns+` FROM turn_ends te WHERE te.session_id=? AND te.completed_turn_id=? AND te.ended_at_ms>0`, sessionID, completedTurnID)
	if err != nil {
		return TurnEnd{}, false, err
	}
	ends, err := scanTurnEnds(rows)
	if err != nil || len(ends) == 0 {
		return TurnEnd{}, false, err
	}
	return ends[0], true, nil
}

// An end stands until a newer turn is opened or a later one completes.
func (r *Registry) CurrentTurnEnds() (map[string]TurnEnd, error) {
	return currentTurnEnds(context.Background(), r.read)
}

func currentTurnEnds(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]TurnEnd, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+turnEndColumns+` FROM turn_ends te WHERE `+currentTurnEnd)
	if err != nil {
		return nil, err
	}
	ends, err := scanTurnEnds(rows)
	if err != nil {
		return nil, err
	}
	out := make(map[string]TurnEnd, len(ends))
	for _, end := range ends {
		out[end.SessionID] = end
	}
	return out, nil
}

const currentTurnEnd = `te.ended_at_ms>0
 AND te.started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=te.session_id)
 AND (te.completed_turn_id='' OR te.completed_turn_id=COALESCE((SELECT completed_turn_id FROM session_runtime WHERE session_id=te.session_id),''))`

const turnEndColumns = `te.session_id,te.started_at_ms,te.ended_at_ms,te.turn_id,te.completed_turn_id,te.needs,te.headline,te.asks_json,te.next_json,te.source,te.routed_to`

func scanTurnEnds(rows *sql.Rows) ([]TurnEnd, error) {
	defer rows.Close()
	var out []TurnEnd
	for rows.Next() {
		var end TurnEnd
		var started, ended int64
		var asks, next string
		if err := rows.Scan(&end.SessionID, &started, &ended, &end.TurnID, &end.CompletedTurnID, &end.Needs, &end.Headline, &asks, &next, &end.Source, &end.RoutedTo); err != nil {
			return nil, err
		}
		end.StartedAt, end.EndedAt = time.UnixMilli(started).UTC(), time.UnixMilli(ended).UTC()
		if err := json.Unmarshal([]byte(asks), &end.Asks); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(next), &end.Next); err != nil {
			return nil, err
		}
		out = append(out, end)
	}
	return out, rows.Err()
}

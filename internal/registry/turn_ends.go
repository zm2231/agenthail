package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type TurnEnd struct {
	SessionID       string
	StartedAt       time.Time
	EndedAt         time.Time
	TurnID          string
	CompletedTurnID string
	Headline        string
	Next            []string
	Source          string
	WaitingOn       string
	WorkItem        string
	LedgerKey       string
	WaitPushed      time.Time
	LeadEscalated   time.Time
}

const TurnSourceTool = "tool"

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

// A turn has started since the last end when one was opened, the last one completed or stopped, or the runtime names another active turn; a later end of the same turn replaces the earlier one.
func (r *Registry) EndTurn(end TurnEnd, activeTurnID string, at time.Time) (TurnEnd, error) {
	next, err := json.Marshal(end.Next)
	if err != nil {
		return TurnEnd{}, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return TurnEnd{}, err
	}
	defer tx.Rollback()
	var started, ended, stopped int64
	var turnID, completed string
	err = tx.QueryRow(`SELECT started_at_ms,ended_at_ms,turn_id,completed_turn_id,stopped_at_ms FROM turn_ends WHERE session_id=? ORDER BY started_at_ms DESC LIMIT 1`, end.SessionID).Scan(&started, &ended, &turnID, &completed, &stopped)
	fresh := errors.Is(err, sql.ErrNoRows) || err == nil && (completed != "" || stopped > 0 || ended > 0 && turnID != "" && activeTurnID != "" && turnID != activeTurnID)
	switch {
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return TurnEnd{}, err
	case fresh:
		started = max(at.UnixMilli(), started+1)
		if _, err := tx.Exec(`INSERT INTO turn_ends(session_id,started_at_ms) VALUES(?,?)`, end.SessionID, started); err != nil {
			return TurnEnd{}, err
		}
	}
	if _, err := tx.Exec(`UPDATE turn_ends SET ended_at_ms=?,turn_id=?,headline=?,next_json=?,source=?,waiting_person=?,work_item=?,ledger_key=? WHERE session_id=? AND started_at_ms=?`,
		at.UnixMilli(), activeTurnID, end.Headline, string(next), end.Source, end.WaitingOn, end.WorkItem, end.LedgerKey, end.SessionID, started); err != nil {
		return TurnEnd{}, err
	}
	if err := tx.Commit(); err != nil {
		return TurnEnd{}, err
	}
	end.StartedAt, end.EndedAt, end.TurnID = time.UnixMilli(started).UTC(), time.UnixMilli(at.UnixMilli()).UTC(), activeTurnID
	return end, nil
}

// StopOpenTurn marks the newest turn of an exited session as stopped when no completion was bound to it, so it is no longer watched; a completion seen later still binds to it. A turn opened after seen, or a session the registry no longer holds as exited, was resumed since the observer saw it exit and keeps its turn.
func (r *Registry) StopOpenTurn(sessionID string, seen, at time.Time) error {
	_, err := r.db.Exec(`UPDATE turn_ends SET stopped_at_ms=? WHERE session_id=? AND completed_turn_id='' AND stopped_at_ms=0 AND started_at_ms<=?
		AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=?)
		AND EXISTS (SELECT 1 FROM sessions s WHERE s.id=? AND (s.ended=1 OR s.status=?))`, at.UnixMilli(), sessionID, seen.UnixMilli(), sessionID, sessionID, string(surface.StatusOffline))
	return err
}

func (r *Registry) MarkTurnTyped(sessionID string, typed bool) error {
	mark := 0
	if typed {
		mark = 1
	}
	_, err := r.db.Exec(`UPDATE turn_ends SET typed=? WHERE session_id=? AND ended_at_ms=0 AND completed_turn_id='' AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=?)`, mark, sessionID, sessionID)
	return err
}

func (r *Registry) TurnTyped(sessionID, completedTurnID string) (bool, error) {
	var typed int
	err := r.read.QueryRow(`SELECT typed FROM turn_ends WHERE session_id=? AND completed_turn_id=? ORDER BY started_at_ms DESC LIMIT 1`, sessionID, completedTurnID).Scan(&typed)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return typed != 0, err
}

// BindTurnCompletion gives a completion to the newest turn opened by the time it completed, so a completion seen only after the next turn opened stays with its own turn. A surface that states the time only to the precision given (Codex: whole seconds) may name a time before its own turn opened; then the oldest unbound turn opened within that precision takes it. A zero completedAt means the surface didn't say, and the newest turn takes it.
func (r *Registry) BindTurnCompletion(sessionID, completedTurnID string, completedAt time.Time, precision time.Duration) error {
	if completedAt.IsZero() {
		_, err := r.db.Exec(`UPDATE turn_ends SET completed_turn_id=? WHERE session_id=? AND completed_turn_id='' AND started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=?)`, completedTurnID, sessionID, sessionID)
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	at := completedAt.UnixMilli()
	var started int64
	var bound string
	err = tx.QueryRow(`SELECT started_at_ms,completed_turn_id FROM turn_ends WHERE session_id=? AND started_at_ms<=? ORDER BY started_at_ms DESC LIMIT 1`, sessionID, at).Scan(&started, &bound)
	switch {
	case err == nil && bound == completedTurnID:
		return nil
	case err == nil && bound == "":
	case err != nil && !errors.Is(err, sql.ErrNoRows):
		return err
	case precision <= time.Millisecond:
		return nil
	default:
		var within sql.NullInt64
		if err := tx.QueryRow(`SELECT MIN(started_at_ms) FROM turn_ends WHERE session_id=? AND completed_turn_id='' AND started_at_ms>? AND started_at_ms<?`, sessionID, at, at+precision.Milliseconds()).Scan(&within); err != nil || !within.Valid {
			return err
		}
		started = within.Int64
	}
	if _, err := tx.Exec(`UPDATE turn_ends SET completed_turn_id=? WHERE session_id=? AND started_at_ms=? AND completed_turn_id=''`, completedTurnID, sessionID, started); err != nil {
		return err
	}
	return tx.Commit()
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

func (r *Registry) CurrentTurnEnd(sessionID string) (TurnEnd, bool, error) {
	rows, err := r.read.Query(`SELECT `+turnEndColumns+` FROM turn_ends te WHERE te.session_id=? AND `+currentTurnEnd, sessionID)
	if err != nil {
		return TurnEnd{}, false, err
	}
	ends, err := scanTurnEnds(rows)
	if err != nil || len(ends) == 0 {
		return TurnEnd{}, false, err
	}
	return ends[0], true, nil
}

func currentTurnEnds(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]TurnEnd, error) {
	rows, err := q.QueryContext(ctx, currentTurnEndsQuery)
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

// Each session's latest turn is found first, so the read does not grow with every turn ever kept.
const currentTurnEndsQuery = `SELECT ` + turnEndColumns + `
 FROM (SELECT s.id AS session_id,(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=s.id) AS started_at_ms FROM sessions s) latest
 CROSS JOIN turn_ends te ON te.session_id=latest.session_id AND te.started_at_ms=latest.started_at_ms
 WHERE te.ended_at_ms>0
 AND (te.completed_turn_id='' OR te.completed_turn_id=COALESCE((SELECT completed_turn_id FROM session_runtime WHERE session_id=te.session_id),''))`

const currentTurnEnd = `te.ended_at_ms>0
 AND te.started_at_ms=(SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=te.session_id)
 AND (te.completed_turn_id='' OR te.completed_turn_id=COALESCE((SELECT completed_turn_id FROM session_runtime WHERE session_id=te.session_id),''))`

const turnEndColumns = `te.session_id,te.started_at_ms,te.ended_at_ms,te.turn_id,te.completed_turn_id,te.headline,te.next_json,te.source,te.waiting_person,te.work_item,te.ledger_key,te.waiting_pushed_at_ms,te.lead_escalated_at_ms`

func scanTurnEnds(rows *sql.Rows) ([]TurnEnd, error) {
	defer rows.Close()
	var out []TurnEnd
	for rows.Next() {
		var end TurnEnd
		var started, ended, pushed, escalated int64
		var next string
		if err := rows.Scan(&end.SessionID, &started, &ended, &end.TurnID, &end.CompletedTurnID, &end.Headline, &next, &end.Source, &end.WaitingOn, &end.WorkItem, &end.LedgerKey, &pushed, &escalated); err != nil {
			return nil, err
		}
		end.StartedAt, end.EndedAt = time.UnixMilli(started).UTC(), time.UnixMilli(ended).UTC()
		if pushed > 0 {
			end.WaitPushed = time.UnixMilli(pushed).UTC()
		}
		end.LeadEscalated = unixMilliOrZero(escalated)
		if err := json.Unmarshal([]byte(next), &end.Next); err != nil {
			return nil, err
		}
		out = append(out, end)
	}
	return out, rows.Err()
}

// ClaimWaitPush takes the one push of a completed turn that ended waiting on the person, while that turn is still the session's last and none runs, and the session's open ask is still askID; a claim is negative until confirmed, so it never reads as pushed before the push is sent.
func (r *Registry) ClaimWaitPush(sessionID, completedTurnID, askID string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE turn_ends SET waiting_pushed_at_ms=? WHERE session_id=? AND completed_turn_id=? AND waiting_person<>'' AND waiting_pushed_at_ms=0
		AND EXISTS (SELECT 1 FROM session_runtime WHERE session_id=? AND completed_turn_id=? AND active_turn_id='')
		AND COALESCE((SELECT (`+rowAsk+`) FROM (SELECT ? AS session_id) s),'')=?`, -at.UnixMilli(), sessionID, completedTurnID, sessionID, completedTurnID, sessionID, askID)
	if err != nil {
		return false, err
	}
	claimed, err := result.RowsAffected()
	return claimed == 1, err
}

func (r *Registry) ConfirmWaitPush(sessionID, completedTurnID string, claimedAt time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE turn_ends SET waiting_pushed_at_ms=? WHERE session_id=? AND completed_turn_id=? AND waiting_pushed_at_ms=?`, claimedAt.UnixMilli(), sessionID, completedTurnID, -claimedAt.UnixMilli())
	if err != nil {
		return false, err
	}
	confirmed, err := result.RowsAffected()
	return confirmed == 1, err
}

func (r *Registry) ReleaseWaitPush(sessionID, completedTurnID string, claimedAt time.Time) error {
	_, err := r.db.Exec(`UPDATE turn_ends SET waiting_pushed_at_ms=0 WHERE session_id=? AND completed_turn_id=? AND waiting_pushed_at_ms=?`, sessionID, completedTurnID, -claimedAt.UnixMilli())
	return err
}

// EscalateTurnEnd moves a completed turn's wait from the lead to the person once, while that turn is still the session's last and none runs.
func (r *Registry) EscalateTurnEnd(sessionID, completedTurnID string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE turn_ends SET lead_escalated_at_ms=? WHERE session_id=? AND completed_turn_id=? AND waiting_person<>'' AND lead_escalated_at_ms=0
		AND EXISTS (SELECT 1 FROM session_runtime WHERE session_id=? AND completed_turn_id=? AND active_turn_id='')`, at.UnixMilli(), sessionID, completedTurnID, sessionID, completedTurnID)
	if err != nil {
		return false, err
	}
	moved, err := result.RowsAffected()
	return moved == 1, err
}

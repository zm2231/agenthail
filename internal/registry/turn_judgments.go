package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

const turnJudgmentRetention = 30 * 24 * time.Hour

type TurnFacts struct {
	BackgroundRunning bool   `json:"backgroundRunning"`
	BackgroundSource  string `json:"backgroundSource,omitempty"`
	BackgroundTasks   int    `json:"backgroundTasks"`
	OpenAsks          int    `json:"openAsks"`
	ChildrenWorking   int    `json:"childrenWorking"`
	AwaitedRelays     int    `json:"awaitedRelays"`
	// AskedOf holds where each open ask is routed: a session id or AskToPerson.
	AskedOf  []string `json:"askedOf,omitempty"`
	Awaiting []string `json:"awaiting,omitempty"`
	Children []string `json:"children,omitempty"`
}

func (f TurnFacts) Pending() bool {
	return f.BackgroundRunning || f.OpenAsks > 0 || f.ChildrenWorking > 0 || f.AwaitedRelays > 0
}

type TurnJudgment struct {
	SessionID    string             `json:"sessionId"`
	TurnID       string             `json:"turnId"`
	EndedAt      time.Time          `json:"endedAt"`
	JudgedAt     time.Time          `json:"judgedAt,omitzero"`
	Model        string             `json:"model,omitempty"`
	PromiseModel string             `json:"promiseModel,omitempty"`
	Ms           int64              `json:"ms,omitempty"`
	Promise      float64            `json:"promise"`
	Needs        float64            `json:"needs"`
	NeedsTail    float64            `json:"needsTail"`
	State        string             `json:"state,omitempty"`
	StateP       map[string]float64 `json:"stateProbabilities,omitempty"`
	Ending       string             `json:"ending"`
	Headline     string             `json:"headline,omitempty"`
	Facts        TurnFacts          `json:"facts"`
	Error        string             `json:"error,omitempty"`
}

func (r *Registry) TurnEndFacts(sessionID string) (TurnFacts, error) {
	var facts TurnFacts
	var err error
	if facts.AskedOf, err = queryRows(r.read, nil, scanString, `SELECT routed_to FROM asks WHERE session_id=? AND state=? ORDER BY created_at_ms,id`, sessionID, AskOpen); err != nil {
		return TurnFacts{}, err
	}
	if facts.Children, err = r.RunningChildren(sessionID); err != nil {
		return TurnFacts{}, err
	}
	if facts.Awaiting, err = queryRows(r.read, nil, scanString, `SELECT DISTINCT r.from_session FROM routes r JOIN sessions s ON s.id=r.from_session WHERE r.to_session=? AND r.active=1 AND r.once_only=1 AND `+liveLocalSession+` ORDER BY r.from_session`, sessionID); err != nil {
		return TurnFacts{}, err
	}
	facts.OpenAsks, facts.ChildrenWorking, facts.AwaitedRelays = len(facts.AskedOf), len(facts.Children), len(facts.Awaiting)
	return facts, nil
}

func (r *Registry) RecordTurnJudgment(j TurnJudgment) error {
	stateP, err := json.Marshal(j.StateP)
	if err != nil {
		return err
	}
	facts, err := json.Marshal(j.Facts)
	if err != nil {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO turn_judgments(session_id, turn_id, ended_at_ms, judged_at_ms, model, promise_model, ms, promise, needs, needs_tail, state, state_json, ending, headline, facts_json, error)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(session_id, turn_id) DO UPDATE SET ended_at_ms=excluded.ended_at_ms, judged_at_ms=excluded.judged_at_ms, model=excluded.model, promise_model=excluded.promise_model, ms=excluded.ms, promise=excluded.promise, needs=excluded.needs, needs_tail=excluded.needs_tail,
		state=excluded.state, state_json=excluded.state_json, ending=excluded.ending, headline=excluded.headline, facts_json=excluded.facts_json, error=excluded.error`,
		j.SessionID, j.TurnID, j.EndedAt.UnixMilli(), milliOrZero(j.JudgedAt), j.Model, j.PromiseModel, j.Ms, j.Promise, j.Needs, j.NeedsTail, j.State, string(stateP), j.Ending, j.Headline, string(facts), j.Error); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM turn_judgments WHERE ended_at_ms<?`, j.EndedAt.Add(-turnJudgmentRetention).UnixMilli()); err != nil {
		return err
	}
	return tx.Commit()
}

const turnJudgmentColumns = `j.session_id, j.turn_id, j.ended_at_ms, j.judged_at_ms, j.model, j.promise_model, j.ms, j.promise, j.needs, j.needs_tail, j.state, j.state_json, j.ending, j.headline, j.facts_json, j.error`

func scanTurnJudgment(row rowScanner) (TurnJudgment, error) {
	var j TurnJudgment
	var ended, judged int64
	var stateP, facts string
	if err := row.Scan(&j.SessionID, &j.TurnID, &ended, &judged, &j.Model, &j.PromiseModel, &j.Ms, &j.Promise, &j.Needs, &j.NeedsTail, &j.State, &stateP, &j.Ending, &j.Headline, &facts, &j.Error); err != nil {
		return TurnJudgment{}, err
	}
	j.EndedAt, j.JudgedAt = time.UnixMilli(ended).UTC(), unixMilliOrZero(judged)
	_ = json.Unmarshal([]byte(stateP), &j.StateP)
	_ = json.Unmarshal([]byte(facts), &j.Facts)
	return j, nil
}

func (r *Registry) TurnJudgments(sessionID string, limit int) ([]TurnJudgment, error) {
	return queryRows(r.read, nil, scanTurnJudgment, `SELECT `+turnJudgmentColumns+`
		FROM turn_judgments j WHERE ?='' OR j.session_id=? ORDER BY j.ended_at_ms DESC, j.rowid DESC LIMIT ?`, sessionID, sessionID, limit)
}

func (r *Registry) CurrentTurnJudgments() (map[string]TurnJudgment, error) {
	return currentTurnJudgments(context.Background(), r.read)
}

func currentTurnJudgments(ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) (map[string]TurnJudgment, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+turnJudgmentColumns+`
		FROM turn_judgments j JOIN session_runtime rt ON rt.session_id=j.session_id AND rt.completed_turn_id=j.turn_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]TurnJudgment{}
	for rows.Next() {
		judgment, err := scanTurnJudgment(rows)
		if err != nil {
			return nil, err
		}
		out[judgment.SessionID] = judgment
	}
	return out, rows.Err()
}

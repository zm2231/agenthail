package registry

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	AskOpen      = "open"
	AskAnswered  = "answered"
	AskWithdrawn = "withdrawn"
	AskExpired   = "expired"

	AskOriginAgent      = "agent"
	AskOriginPermission = "permission"

	AskToPerson = "person"

	AskRaised    = "raised"
	AskEscalated = "escalated"

	MaxAskTitle    = 80
	MaxAskQuestion = 160
	MaxAskLabel    = 40
	MaxAskDoes     = 90
)

type AskOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Does  string `json:"does,omitempty"`
}

type AskLink struct {
	Title string `json:"title"`
	URL   string `json:"url"`
}

type AskQuestion struct {
	Question    string      `json:"question"`
	Options     []AskOption `json:"options"`
	Recommended string      `json:"recommended,omitempty"`
	Links       []AskLink   `json:"links,omitempty"`
}

type AskHop struct {
	To  string    `json:"to"`
	At  time.Time `json:"at"`
	Why string    `json:"why"`
}

type AskAnswer struct {
	Answers map[string]string `json:"answers,omitempty"`
	Note    string            `json:"note,omitempty"`
}

type Ask struct {
	ID          string
	SessionID   string
	Origin      string
	Title       string
	Questions   []AskQuestion
	RoutedTo    string
	RoutedAt    time.Time
	Route       []AskHop
	State       string
	Answer      AskAnswer
	AnsweredBy  string
	AnsweredVia string
	AnsweredAt  time.Time
	AppliedAt   time.Time
	ApplyStatus string
	ApplyID     string
	CreatedAt   time.Time
	Ref         string
	WaitKind    surface.WaitingKind
	Answerable  bool
	AnnouncedAt time.Time
	Deadline    time.Time
}

func newAskID() (string, error) {
	raw := make([]byte, 6)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return "ask-" + hex.EncodeToString(raw), nil
}

func (r *Registry) RaiseAsk(ask Ask, at time.Time) (Ask, error) {
	id, err := newAskID()
	if err != nil {
		return Ask{}, err
	}
	at = time.UnixMilli(at.UnixMilli()).UTC()
	ask.ID, ask.State, ask.CreatedAt, ask.RoutedAt = id, AskOpen, at, at
	ask.Route = []AskHop{{To: ask.RoutedTo, At: at, Why: AskRaised}}
	questions, err := json.Marshal(ask.Questions)
	if err != nil {
		return Ask{}, err
	}
	route, err := json.Marshal(ask.Route)
	if err != nil {
		return Ask{}, err
	}
	_, err = r.db.Exec(`INSERT INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,answerable) VALUES(?,?,?,?,?,?,?,?,?,?,1)`,
		ask.ID, ask.SessionID, ask.Origin, ask.Title, string(questions), ask.RoutedTo, at.UnixMilli(), string(route), AskOpen, at.UnixMilli())
	if err != nil {
		return Ask{}, err
	}
	return ask, nil
}

func (r *Registry) Ask(id string) (Ask, bool, error) {
	asks, err := queryAsks(context.Background(), r.read, `WHERE id=?`, id)
	if err != nil || len(asks) == 0 {
		return Ask{}, false, err
	}
	return asks[0], true, nil
}

func (r *Registry) OpenAsks() ([]Ask, error) {
	return queryAsks(context.Background(), r.read, `WHERE state=? ORDER BY created_at_ms, id`, AskOpen)
}

func (r *Registry) SessionAsks(sessionID string) ([]Ask, error) {
	return queryAsks(context.Background(), r.read, `WHERE session_id=? ORDER BY created_at_ms, id`, sessionID)
}

func (r *Registry) AskRaisedSince(sessionID string, since time.Time) (bool, error) {
	var raised bool
	err := r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM asks WHERE session_id=? AND origin='agent' AND created_at_ms>=?)`, sessionID, since.UnixMilli()).Scan(&raised)
	return raised, err
}

// ClaimAsk answers an open ask at most once; false means it was already answered or withdrawn.
func (r *Registry) ClaimAsk(id string, answer AskAnswer, by, via string, at time.Time) (bool, error) {
	encoded, err := json.Marshal(answer)
	if err != nil {
		return false, err
	}
	result, err := r.db.Exec(`UPDATE asks SET state=?,answer_json=?,answered_by=?,answered_via=?,answered_at_ms=? WHERE id=? AND state=?`,
		AskAnswered, string(encoded), by, via, at.UnixMilli(), id, AskOpen)
	if err != nil {
		return false, err
	}
	claimed, err := result.RowsAffected()
	return claimed == 1, err
}

// ReleaseAsk reopens an ask whose answer provably never reached the session.
func (r *Registry) ReleaseAsk(id string) error {
	_, err := r.db.Exec(`UPDATE asks SET state=?,answer_json='',answered_by='',answered_via='',answered_at_ms=0 WHERE id=? AND state=? AND applied_at_ms=0`, AskOpen, id, AskAnswered)
	return err
}

func (r *Registry) RecordAskApplied(id, status, applyID string, at time.Time) error {
	_, err := r.db.Exec(`UPDATE asks SET applied_at_ms=?,apply_status=?,apply_id=? WHERE id=? AND state=?`, at.UnixMilli(), status, applyID, id, AskAnswered)
	return err
}

func (r *Registry) WithdrawAsk(id string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, at.UnixMilli(), id, AskOpen)
	if err != nil {
		return false, err
	}
	withdrawn, err := result.RowsAffected()
	return withdrawn == 1, err
}

func (r *Registry) RouteAsk(id, to, why string, at time.Time) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var routedTo, route string
	err = tx.QueryRow(`SELECT routed_to,route_json FROM asks WHERE id=? AND state=?`, id, AskOpen).Scan(&routedTo, &route)
	if errors.Is(err, sql.ErrNoRows) || err == nil && routedTo == to {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var hops []AskHop
	if err := json.Unmarshal([]byte(route), &hops); err != nil {
		return false, err
	}
	at = time.UnixMilli(at.UnixMilli()).UTC()
	encoded, err := json.Marshal(append(hops, AskHop{To: to, At: at, Why: why}))
	if err != nil {
		return false, err
	}
	if _, err := tx.Exec(`UPDATE asks SET routed_to=?,routed_at_ms=?,route_json=? WHERE id=?`, to, at.UnixMilli(), string(encoded), id); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

// A session's row shows its oldest open ask, else its newest answered one until the session starts another turn.
const rowAsk = `SELECT a.id FROM asks a WHERE a.session_id=s.session_id AND a.origin IN ('agent','delivery','ledger') AND (a.state='open'
 OR a.state='answered' AND a.answered_at_ms>=COALESCE((SELECT MAX(started_at_ms) FROM turn_ends WHERE session_id=a.session_id),0))
 ORDER BY a.state='open' DESC, CASE WHEN a.state='open' THEN a.created_at_ms ELSE -a.answered_at_ms END, a.id LIMIT 1`

func rowAsks(ctx context.Context, q askQuerier) (map[string]Ask, error) {
	asks, err := queryAsks(ctx, q, `WHERE id IN (SELECT (`+rowAsk+`) FROM (SELECT DISTINCT session_id FROM asks) s)`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]Ask, len(asks))
	for _, ask := range asks {
		out[ask.SessionID] = ask
	}
	return out, nil
}

func (r *Registry) RowAsks() (map[string]Ask, error) {
	return rowAsks(context.Background(), r.read)
}

func (r *Registry) RowAsk(sessionID string) (Ask, bool, error) {
	asks, err := queryAsks(context.Background(), r.read, `WHERE id=(SELECT (`+rowAsk+`) FROM (SELECT ? AS session_id) s)`, sessionID)
	if err != nil || len(asks) == 0 {
		return Ask{}, false, err
	}
	return asks[0], true, nil
}

type askQuerier interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

const askColumns = `id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,answer_json,answered_by,answered_via,answered_at_ms,applied_at_ms,apply_status,apply_id,created_at_ms,ref,wait_kind,answerable,announced_at_ms,deadline_ms`

func queryAsks(ctx context.Context, q askQuerier, where string, args ...any) ([]Ask, error) {
	rows, err := q.QueryContext(ctx, `SELECT `+askColumns+` FROM asks `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Ask
	for rows.Next() {
		var ask Ask
		var questions, route, answer string
		var routed, answered, applied, created, announced, deadline int64
		var kind string
		if err := rows.Scan(&ask.ID, &ask.SessionID, &ask.Origin, &ask.Title, &questions, &ask.RoutedTo, &routed, &route, &ask.State, &answer, &ask.AnsweredBy, &ask.AnsweredVia, &answered, &applied, &ask.ApplyStatus, &ask.ApplyID, &created, &ask.Ref, &kind, &ask.Answerable, &announced, &deadline); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(questions), &ask.Questions); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(route), &ask.Route); err != nil {
			return nil, err
		}
		if answer != "" {
			if err := json.Unmarshal([]byte(answer), &ask.Answer); err != nil {
				return nil, err
			}
		}
		ask.RoutedAt, ask.CreatedAt = time.UnixMilli(routed).UTC(), time.UnixMilli(created).UTC()
		if answered > 0 {
			ask.AnsweredAt = time.UnixMilli(answered).UTC()
		}
		if applied > 0 {
			ask.AppliedAt = time.UnixMilli(applied).UTC()
		}
		if announced > 0 {
			ask.AnnouncedAt = time.UnixMilli(announced).UTC()
		}
		if deadline > 0 {
			ask.Deadline = time.UnixMilli(deadline).UTC()
		}
		ask.WaitKind = surface.WaitingKind(kind)
		out = append(out, ask)
	}
	return out, rows.Err()
}

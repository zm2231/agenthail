package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const (
	AskOriginApproval = "approval"

	ApprovalAllow = "allow"
	ApprovalDeny  = "deny"

	ApprovalRan     = "ran"
	ApprovalFailed  = "failed"
	ApprovalDenied  = "denied"
	ApprovalExpired = "expired"
	ApprovalNotRun  = "not_run"
)

type Approval struct {
	AskID       string
	SessionID   string
	API         string
	Payload     json.RawMessage
	PayloadHash string
	Description string
	Reason      string
	CreatedAt   time.Time
	ExpiresAt   time.Time
	DecidedBy   string
	DecidedAt   time.Time
	RanAt       time.Time
	Status      string
	Result      string
}

func ApprovalPayloadHash(api string, payload []byte) string {
	sum := sha256.Sum256(append([]byte(api+"\n"), payload...))
	return hex.EncodeToString(sum[:])
}

// An open approval with the same payload is returned instead of a second one.
func (r *Registry) RaiseApproval(ask Ask, approval Approval, at time.Time) (Ask, bool, error) {
	at = time.UnixMilli(at.UnixMilli()).UTC()
	approval.PayloadHash = ApprovalPayloadHash(approval.API, approval.Payload)
	tx, err := r.db.Begin()
	if err != nil {
		return Ask{}, false, err
	}
	defer tx.Rollback()
	open, err := queryAsks(context.Background(), tx, `WHERE origin=? AND state=? AND session_id=? AND id IN (SELECT ask_id FROM approvals WHERE payload_sha256=?)`, AskOriginApproval, AskOpen, approval.SessionID, approval.PayloadHash)
	if err != nil {
		return Ask{}, false, err
	}
	if len(open) > 0 {
		return open[0], false, nil
	}
	id, err := newAskID()
	if err != nil {
		return Ask{}, false, err
	}
	ask.ID, ask.SessionID, ask.Origin, ask.State, ask.RoutedTo, ask.CreatedAt, ask.RoutedAt = id, approval.SessionID, AskOriginApproval, AskOpen, AskToPerson, at, at
	ask.Deadline = time.UnixMilli(approval.ExpiresAt.UnixMilli()).UTC()
	ask.Route = []AskHop{{To: AskToPerson, At: at, Why: AskRaised}}
	questions, err := json.Marshal(ask.Questions)
	if err != nil {
		return Ask{}, false, err
	}
	route, err := json.Marshal(ask.Route)
	if err != nil {
		return Ask{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,ref,deadline_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		ask.ID, ask.SessionID, AskOriginApproval, ask.Title, string(questions), AskToPerson, at.UnixMilli(), string(route), AskOpen, at.UnixMilli(), ask.ID, ask.Deadline.UnixMilli()); err != nil {
		return Ask{}, false, err
	}
	ask.Ref = ask.ID
	if _, err := tx.Exec(`INSERT INTO approvals(ask_id,session_id,api,payload,payload_sha256,description,reason,created_at_ms,expires_at_ms) VALUES(?,?,?,?,?,?,?,?,?)`,
		ask.ID, approval.SessionID, approval.API, string(approval.Payload), approval.PayloadHash, approval.Description, approval.Reason, at.UnixMilli(), ask.Deadline.UnixMilli()); err != nil {
		return Ask{}, false, err
	}
	return ask, true, tx.Commit()
}

func (r *Registry) Approval(askID string) (Approval, bool, error) {
	var a Approval
	var payload string
	var created, expires, decided, ran int64
	err := r.read.QueryRow(`SELECT ask_id,session_id,api,payload,payload_sha256,description,reason,created_at_ms,expires_at_ms,decided_by,decided_at_ms,ran_at_ms,status,result FROM approvals WHERE ask_id=?`, askID).
		Scan(&a.AskID, &a.SessionID, &a.API, &payload, &a.PayloadHash, &a.Description, &a.Reason, &created, &expires, &a.DecidedBy, &decided, &ran, &a.Status, &a.Result)
	if errors.Is(err, sql.ErrNoRows) {
		return Approval{}, false, nil
	}
	if err != nil {
		return Approval{}, false, err
	}
	a.Payload = json.RawMessage(payload)
	a.CreatedAt, a.ExpiresAt = time.UnixMilli(created).UTC(), time.UnixMilli(expires).UTC()
	if decided > 0 {
		a.DecidedAt = time.UnixMilli(decided).UTC()
	}
	if ran > 0 {
		a.RanAt = time.UnixMilli(ran).UTC()
	}
	return a, true, nil
}

func (r *Registry) DecideApproval(askID, decision, question, by, via string, at time.Time) (bool, error) {
	encoded, err := json.Marshal(AskAnswer{Answers: map[string]string{question: decision}})
	if err != nil {
		return false, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	result, err := tx.Exec(`UPDATE asks SET state=?,answer_json=?,answered_by=?,answered_via=?,answered_at_ms=? WHERE id=? AND origin=? AND state=? AND EXISTS(SELECT 1 FROM approvals WHERE ask_id=asks.id AND expires_at_ms>?)`,
		AskAnswered, string(encoded), AskToPerson, via, at.UnixMilli(), askID, AskOriginApproval, AskOpen, at.UnixMilli())
	if err != nil {
		return false, err
	}
	if claimed, err := result.RowsAffected(); err != nil || claimed != 1 {
		return false, err
	}
	status := ""
	if decision == ApprovalDeny {
		status = ApprovalDenied
	}
	if _, err := tx.Exec(`UPDATE approvals SET decided_by=?,decided_at_ms=?,status=? WHERE ask_id=?`, by, at.UnixMilli(), status, askID); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (r *Registry) RecordApprovalRun(askID, status, result string, at time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE approvals SET ran_at_ms=?,status=?,result=? WHERE ask_id=?`, at.UnixMilli(), status, result, askID); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE asks SET applied_at_ms=?,apply_status=? WHERE id=? AND state=?`, at.UnixMilli(), status, askID, AskAnswered); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) ExpireApprovals(now time.Time) ([]Ask, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	due, err := queryAsks(context.Background(), tx, `WHERE origin=? AND state=? AND deadline_ms>0 AND deadline_ms<=? ORDER BY created_at_ms, id`, AskOriginApproval, AskOpen, now.UnixMilli())
	if err != nil || len(due) == 0 {
		return nil, err
	}
	for i, ask := range due {
		if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskExpired, now.UnixMilli(), ask.ID, AskOpen); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`UPDATE approvals SET status=? WHERE ask_id=?`, ApprovalExpired, ask.ID); err != nil {
			return nil, err
		}
		due[i].State = AskExpired
	}
	return due, tx.Commit()
}

func (r *Registry) UnranApprovals() ([]Approval, error) {
	rows, err := r.read.Query(`SELECT ask_id FROM approvals WHERE decided_at_ms>0 AND status='' ORDER BY created_at_ms`)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := make([]Approval, 0, len(ids))
	for _, id := range ids {
		approval, found, err := r.Approval(id)
		if err != nil {
			return nil, err
		}
		if found {
			out = append(out, approval)
		}
	}
	return out, nil
}

package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"
)

const (
	AskOriginLedger   = "ledger"
	LedgerRecommended = "recommended"
	LedgerDefault     = "default"
)

type LedgerQuestion struct {
	ID             string
	OwnerSessionID string
	Text           string
	Recommendation string
	DefaultAnswer  string
	Deadline       time.Time
	Answered       bool
	Defaulted      bool
}

func LedgerAskRef(projectKey, questionID string) string {
	return "ledger:" + projectKey + ":" + questionID
}

func ParseLedgerAskRef(ref string) (projectKey, questionID string, ok bool) {
	rest, found := strings.CutPrefix(ref, "ledger:")
	at := strings.LastIndex(rest, ":")
	if !found || at <= 0 || at == len(rest)-1 {
		return "", "", false
	}
	return rest[:at], rest[at+1:], true
}

func ledgerOption(id, text string) AskOption {
	option := AskOption{ID: id, Label: clip(text, MaxAskLabel)}
	if option.Label != text {
		option.Does = clip(text, MaxAskDoes)
	}
	return option
}

func ledgerAskQuestion(q LedgerQuestion) AskQuestion {
	question := AskQuestion{Question: clip(q.Text, MaxAskQuestion), Options: []AskOption{ledgerOption(LedgerRecommended, q.Recommendation)}, Recommended: LedgerRecommended}
	if q.DefaultAnswer != "" && !strings.EqualFold(q.DefaultAnswer, q.Recommendation) {
		question.Options = append(question.Options, ledgerOption(LedgerDefault, q.DefaultAnswer))
	}
	return question
}

func (r *Registry) SyncLedgerAsks(projectKey string, questions []LedgerQuestion, now time.Time) ([]Ask, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	ctx := context.Background()
	prefix := LedgerAskRef(projectKey, "")
	open, err := queryAsks(ctx, tx, `WHERE origin=? AND state=? AND substr(ref,1,?)=?`, AskOriginLedger, AskOpen, len(prefix), prefix)
	if err != nil {
		return nil, err
	}
	openByRef := make(map[string]Ask, len(open))
	for _, ask := range open {
		openByRef[ask.Ref] = ask
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	var expired []Ask
	for _, q := range questions {
		ref := LedgerAskRef(projectKey, q.ID)
		ask, asked := openByRef[ref]
		switch {
		case q.Defaulted && asked:
			if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskExpired, at.UnixMilli(), ask.ID, AskOpen); err != nil {
				return nil, err
			}
			ask.State, ask.AnsweredAt = AskExpired, at
			expired = append(expired, ask)
		case q.Answered && asked:
			if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, at.UnixMilli(), ask.ID, AskOpen); err != nil {
				return nil, err
			}
		case !q.Answered && !q.Defaulted && !asked:
			if err := raiseLedgerAsk(tx, ref, q, at); err != nil {
				return nil, err
			}
		}
	}
	return expired, tx.Commit()
}

func raiseLedgerAsk(tx *sql.Tx, ref string, q LedgerQuestion, at time.Time) error {
	var local bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sessions WHERE id=? AND host_id='')`, q.OwnerSessionID).Scan(&local); err != nil || !local {
		return err
	}
	id, err := newAskID()
	if err != nil {
		return err
	}
	questions, err := json.Marshal([]AskQuestion{ledgerAskQuestion(q)})
	if err != nil {
		return err
	}
	route, err := json.Marshal([]AskHop{{To: AskToPerson, At: at, Why: AskRaised}})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT OR IGNORE INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,ref,deadline_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`,
		id, q.OwnerSessionID, AskOriginLedger, clip(q.ID+": "+q.Text, MaxAskTitle), string(questions), AskToPerson, at.UnixMilli(), string(route), AskOpen, at.UnixMilli(), ref, q.Deadline.UnixMilli())
	return err
}

func (r *Registry) UntoldLedgerAsks() ([]Ask, error) {
	return queryAsks(context.Background(), r.read, `WHERE origin=? AND state=? AND announced_at_ms=0 ORDER BY created_at_ms, id`, AskOriginLedger, AskOpen)
}

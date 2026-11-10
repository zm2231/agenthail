package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type turnEndAsk struct {
	Question string `json:"question"`
	Options  []struct {
		Label string `json:"label"`
		Does  string `json:"does,omitempty"`
	} `json:"options"`
	Suggested *int      `json:"suggested,omitempty"`
	Links     []AskLink `json:"links,omitempty"`
}

// moveTurnEndAsks turns each standing, unanswered needs-you end into an open ask, then drops the end's ask columns.
func moveTurnEndAsks(tx *sql.Tx) error {
	asked, err := columnExists(tx, "turn_ends", "asks_json")
	if err != nil || !asked {
		return err
	}
	answered, err := columnExists(tx, "turn_ends", "answered_at_ms")
	if err != nil {
		return err
	}
	unanswered := ""
	if answered {
		unanswered = " AND te.answered_at_ms=0"
	}
	rows, err := tx.Query(`SELECT te.session_id,te.ended_at_ms,te.headline,te.asks_json,te.routed_to FROM turn_ends te WHERE te.needs='you'` + unanswered + ` AND ` + currentTurnEnd)
	if err != nil {
		return err
	}
	type standing struct {
		session, headline, asks, routedTo string
		ended                             int64
	}
	var ends []standing
	for rows.Next() {
		var end standing
		if err := rows.Scan(&end.session, &end.ended, &end.headline, &end.asks, &end.routedTo); err != nil {
			rows.Close()
			return err
		}
		ends = append(ends, end)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, end := range ends {
		var old []turnEndAsk
		if err := json.Unmarshal([]byte(end.asks), &old); err != nil || len(old) == 0 {
			continue
		}
		questions := make([]AskQuestion, 0, len(old))
		for _, ask := range old {
			question := AskQuestion{Question: ask.Question, Links: ask.Links}
			for i, option := range ask.Options {
				question.Options = append(question.Options, AskOption{ID: fmt.Sprintf("o%d", i), Label: option.Label, Does: option.Does})
			}
			if ask.Suggested != nil && *ask.Suggested >= 0 && *ask.Suggested < len(question.Options) {
				question.Recommended = question.Options[*ask.Suggested].ID
			}
			questions = append(questions, question)
		}
		to := end.routedTo
		if to == "" {
			to = AskToPerson
		}
		at := time.UnixMilli(end.ended).UTC()
		id, err := newAskID()
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(questions)
		if err != nil {
			return err
		}
		route, err := json.Marshal([]AskHop{{To: to, At: at, Why: AskRaised}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			id, end.session, AskOriginAgent, end.headline, string(encoded), to, end.ended, string(route), AskOpen, end.ended); err != nil {
			return err
		}
	}
	const columns = `session_id,started_at_ms,ended_at_ms,turn_id,completed_turn_id,headline,next_json,source`
	for _, step := range []string{
		`ALTER TABLE turn_ends RENAME TO turn_ends_asked`,
		schema,
		`INSERT INTO turn_ends (` + columns + `) SELECT ` + columns + ` FROM turn_ends_asked`,
		`DROP TABLE turn_ends_asked`,
	} {
		if _, err := tx.Exec(step); err != nil {
			return err
		}
	}
	return nil
}

func addAskRefs(tx *sql.Tx) error {
	for _, column := range []struct{ name, decl string }{
		{"ref", `TEXT NOT NULL DEFAULT ''`},
		{"wait_kind", `TEXT NOT NULL DEFAULT ''`},
		{"answerable", `INTEGER NOT NULL DEFAULT 1`},
		{"announced_at_ms", `INTEGER NOT NULL DEFAULT 0`},
	} {
		if err := ensureColumn(tx, "asks", column.name, column.decl); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS asks_ref ON asks(session_id, origin, ref) WHERE ref<>'' AND state<>'withdrawn'`); err != nil {
		return err
	}
	for _, column := range []string{"waiting_kind", "waiting_id", "waiting_since_ms", "waiting_announced", "waiting_answerable"} {
		exists, err := columnExists(tx, "session_runtime", column)
		if err != nil {
			return err
		}
		if !exists {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE session_runtime DROP COLUMN ` + column); err != nil {
			return err
		}
	}
	return nil
}

func moveDeliveryProblemsToAsks(tx *sql.Tx) error {
	var found [4]bool
	for i, column := range []struct{ table, name string }{
		{"attention_items", "id"},
		{"delivery_intents", "dismissed_at"},
		{"delivery_intents", "notification_queue_id"},
		{"message_queue", "refusal_announced"},
	} {
		exists, err := columnExists(tx, column.table, column.name)
		if err != nil {
			return err
		}
		found[i] = exists
	}
	attention, dismissable, notified, announced := found[0], found[1], found[2], found[3]
	if !attention && !dismissable && !notified && !announced {
		return nil
	}
	now := time.Now()
	if err := syncDeliveryAsksTx(tx, now); err != nil {
		return err
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	if dismissable {
		if _, err := tx.Exec(`UPDATE asks SET state=?,answered_by=?,answered_at_ms=?,applied_at_ms=?,apply_status='applied',
			answer_json=json_object('answers',json_object(json_extract(questions_json,'$[0].question'),'Dismiss'))
			WHERE origin=? AND state=? AND EXISTS (SELECT 1 FROM delivery_intents di WHERE COALESCE(di.dismissed_at,'')!='' AND asks.ref LIKE 'intent:' || di.id || ':%')`,
			AskAnswered, AskToPerson, at.UnixMilli(), at.UnixMilli(), AskOriginDelivery, AskOpen); err != nil {
			return err
		}
	}
	open, err := queryAsks(context.Background(), tx, `WHERE origin=? AND state=? AND routed_to<>?`, AskOriginDelivery, AskOpen, AskToPerson)
	if err != nil {
		return err
	}
	route, err := json.Marshal([]AskHop{{To: AskToPerson, At: at, Why: AskRaised}})
	if err != nil {
		return err
	}
	for _, ask := range open {
		if _, err := tx.Exec(`UPDATE asks SET routed_to=?,route_json=? WHERE id=?`, AskToPerson, string(route), ask.ID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`DROP TABLE IF EXISTS attention_items`); err != nil {
		return err
	}
	if dismissable || notified {
		const columns = `id,sender_session_id,target_session_id,provider_key,message,queue_id,relayed,uuid,status,evidence,failure,failed_from,retries,created_at,updated_at`
		for _, step := range []string{
			`ALTER TABLE delivery_intents RENAME TO delivery_intents_dismissable`,
			schema,
			`INSERT INTO delivery_intents (` + columns + `) SELECT ` + columns + ` FROM delivery_intents_dismissable`,
			`DROP TABLE delivery_intents_dismissable`,
			schema,
			`CREATE UNIQUE INDEX IF NOT EXISTS delivery_intents_uuid ON delivery_intents(uuid)`,
		} {
			if _, err := tx.Exec(step); err != nil {
				return err
			}
		}
	}
	if !announced {
		return nil
	}
	_, err = tx.Exec(`ALTER TABLE message_queue DROP COLUMN refusal_announced`)
	return err
}

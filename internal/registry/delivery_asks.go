package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const AskOriginDelivery = "delivery"

const (
	DeliveryRetry   = "retry"
	DeliveryDelete  = "delete"
	DeliveryDismiss = "dismiss"
	DeliveryCancel  = "cancel"
)

type DeliveryRef struct {
	IntentID int64
	QueueID  int64
	Kind     string
}

const (
	deliveryFailed  = "failed"
	deliveryHeld    = "held"
	deliveryDead    = "dead"
	deliveryRefused = "refused"
)

func ParseDeliveryRef(ref string) (DeliveryRef, bool) {
	parts := strings.Split(ref, ":")
	if len(parts) < 3 {
		return DeliveryRef{}, false
	}
	id, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || id <= 0 {
		return DeliveryRef{}, false
	}
	switch {
	case parts[0] == "intent" && len(parts) == 4 && (parts[3] == deliveryFailed || parts[3] == deliveryHeld):
		return DeliveryRef{IntentID: id, Kind: parts[3]}, true
	case parts[0] == "queue" && (parts[2] == deliveryDead && len(parts) == 3 || parts[2] == deliveryRefused && len(parts) == 4 || parts[2] == DeliveryShared && len(parts) == 5):
		return DeliveryRef{QueueID: id, Kind: parts[2]}, true
	}
	return DeliveryRef{}, false
}

type deliveryProblem struct {
	ref       string
	sessionID string
	routedTo  string
	title     string
	question  AskQuestion
}

func deliveryReasonOf(column string) string {
	return strings.ReplaceAll(deliveryReason, "last_error", column)
}

const deliveryReason = `CASE
	WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 404%' THEN 'The target is unavailable'
	WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 401%' THEN 'Claude needs to reconnect'
	WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 403%' THEN 'Claude denied this delivery'
	WHEN last_error LIKE 'delivery outcome is unknown:%HTTP 400%' THEN 'The delivery request needs correction'
	WHEN last_error LIKE 'delivery outcome is unknown%' THEN 'Its delivery could not be confirmed'
	WHEN last_error LIKE 'delivery rejected [target missing]:%' THEN 'The target is unavailable'
	WHEN last_error LIKE 'delivery rejected [authentication needed]:%' THEN 'Claude needs to reconnect'
	WHEN last_error LIKE 'delivery rejected [access denied]:%' THEN 'Claude denied this delivery'
	WHEN last_error LIKE 'delivery rejected [invalid request]:%' THEN 'The delivery request needs correction'
	WHEN ` + hostUnprovenRow + ` THEN substr(last_error, length('` + hostUnprovenPrefix + `') + 1)
	WHEN last_error!='' THEN last_error
	ELSE 'It failed'
END`

// A relay source never sent the message: telling it would start a turn the same relay forwards again.
func deliveryRoute(sender, target string, relayed bool) (sessionID, routedTo string) {
	if relayed || !readsNotices(sender) {
		return target, AskToPerson
	}
	return sender, sender
}

func deliveryOption(id string) AskOption {
	switch id {
	case DeliveryRetry:
		return AskOption{ID: id, Label: "Retry", Does: "send it again"}
	case DeliveryDelete:
		return AskOption{ID: id, Label: "Delete", Does: "it never reached the session; remove it"}
	case DeliveryCancel:
		return AskOption{ID: id, Label: "Cancel", Does: "stop trying to deliver it"}
	}
	return AskOption{ID: DeliveryDismiss, Label: "Dismiss", Does: "leave it as it is"}
}

func deliveryQuestion(message, reason string, options ...string) AskQuestion {
	question := AskQuestion{Question: fmt.Sprintf("%s: %q", reason, clip(strings.Join(strings.Fields(message), " "), maxQuotedMessage))}
	for _, id := range options {
		question.Options = append(question.Options, deliveryOption(id))
	}
	question.Recommended = question.Options[0].ID
	return question
}

const maxQuotedMessage = 120

func clip(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max-1]) + "…"
}

func deliveryProblems(ctx context.Context, tx *sql.Tx, now time.Time) ([]deliveryProblem, error) {
	var problems []deliveryProblem
	rows, err := tx.QueryContext(ctx, `SELECT di.id,di.sender_session_id,di.target_session_id,di.relayed,di.status,di.retries,di.message,CASE WHEN di.status='held' THEN di.failure ELSE `+deliveryReasonOf("di.failure")+` END,(`+deletableFailure+`),
		EXISTS(SELECT 1 FROM message_queue mq WHERE mq.id=di.queue_id AND di.queue_id!=0 AND mq.status='dead')
		FROM delivery_intents di
		WHERE di.status='held' OR di.status='failed' AND di.updated_at>=datetime('now','-1 day') AND NOT (`+settledDeliveryFailure+`)
		ORDER BY di.id`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var sender, target, status, message, failure string
		var relayed, deletable, retryable bool
		var retries int
		if err := rows.Scan(&id, &sender, &target, &relayed, &status, &retries, &message, &failure, &deletable, &retryable); err != nil {
			rows.Close()
			return nil, err
		}
		problem := deliveryProblem{ref: fmt.Sprintf("intent:%d:%d:%s", id, retries, status)}
		problem.sessionID, problem.routedTo = deliveryRoute(sender, target, relayed)
		if status == deliveryHeld {
			problem.title = "Message held by its target"
			problem.question = deliveryQuestion(message, failure, DeliveryDismiss)
		} else {
			problem.title = "Message not delivered"
			options := []string{}
			if retryable {
				options = append(options, DeliveryRetry)
			}
			if deletable {
				options = append(options, DeliveryDelete)
			}
			problem.question = deliveryQuestion(message, failure, append(options, DeliveryDismiss)...)
		}
		problems = append(problems, problem)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT mq.id,mq.session_id,mq.source_session_id,COALESCE(di.relayed,0),mq.message,mq.refusal,mq.refused_now,mq.reason,mq.holders_json
		FROM (SELECT id,session_id,source_session_id,message,refusal,`+queueRefusedRow+` AS refused_now,`+deliveryReason+` AS reason,holders_json
			FROM message_queue WHERE status='dead' AND dead_at_ms>? OR `+queueRefusedRow+`) mq
		LEFT JOIN delivery_intents di ON di.queue_id=mq.id
		WHERE COALESCE(di.status,'') NOT IN ('failed','held')
		ORDER BY mq.id`, now.Add(-deadReviewWindow).UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, refusal int64
		var target, sender, message, reason, rawHolders string
		var relayed, refused bool
		if err := rows.Scan(&id, &target, &sender, &relayed, &message, &refusal, &refused, &reason, &rawHolders); err != nil {
			return nil, err
		}
		var holders []surface.Holder
		if err := json.Unmarshal([]byte(rawHolders), &holders); err != nil {
			return nil, err
		}
		var problem deliveryProblem
		problem.sessionID, problem.routedTo = deliveryRoute(sender, target, relayed)
		if refused && len(holders) > 1 {
			problem.ref, problem.title, problem.question = sharedProblem(id, refusal, message, holders)
		} else if refused {
			problem.ref = fmt.Sprintf("queue:%d:%s:%d", id, deliveryRefused, refusal)
			problem.title = "Target refused to start"
			problem.question = deliveryQuestion(message, reason, DeliveryCancel, DeliveryDismiss)
		} else {
			problem.ref = fmt.Sprintf("queue:%d:%s", id, deliveryDead)
			problem.title = "Message not confirmed"
			problem.question = deliveryQuestion(message, reason, DeliveryRetry, DeliveryDismiss)
		}
		problems = append(problems, problem)
	}
	return problems, rows.Err()
}

const deliveryAsksInPlay = `SELECT EXISTS(SELECT 1 FROM delivery_intents WHERE status='held')
	OR EXISTS(SELECT 1 FROM delivery_intents WHERE status='failed' AND updated_at>=datetime('now','-1 day'))
	OR EXISTS(SELECT 1 FROM message_queue WHERE status='dead' AND dead_at_ms>?)
	OR EXISTS(SELECT 1 FROM message_queue WHERE ` + queueRefusedRow + `)
	OR EXISTS(SELECT 1 FROM asks WHERE origin='delivery' AND state='open')`

func (r *Registry) SyncDeliveryAsks(now time.Time) error {
	if open, err := r.readExists(deliveryAsksInPlay, now.Add(-deadReviewWindow).UnixMilli()); err != nil || !open {
		return err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := syncDeliveryAsksTx(tx, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) deliveryWrite(write func(*sql.Tx) error) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := write(tx); err != nil {
		return err
	}
	if err := syncDeliveryAsksTx(tx, r.now()); err != nil {
		return err
	}
	return tx.Commit()
}

func syncDeliveryAsksTx(tx *sql.Tx, now time.Time) error {
	var inPlay bool
	if err := tx.QueryRow(deliveryAsksInPlay, now.Add(-deadReviewWindow).UnixMilli()).Scan(&inPlay); err != nil || !inPlay {
		return err
	}
	ctx := context.Background()
	problems, err := deliveryProblems(ctx, tx, now)
	if err != nil {
		return err
	}
	open, err := queryAsks(ctx, tx, `WHERE origin=? AND state=?`, AskOriginDelivery, AskOpen)
	if err != nil {
		return err
	}
	at := time.UnixMilli(now.UnixMilli()).UTC()
	wanted := make(map[string]bool, len(problems))
	for _, problem := range problems {
		wanted[problem.sessionID+"\x00"+problem.ref] = true
	}
	for _, ask := range open {
		if wanted[ask.SessionID+"\x00"+ask.Ref] {
			continue
		}
		if _, err := tx.Exec(`UPDATE asks SET state=?,answered_at_ms=? WHERE id=? AND state=?`, AskWithdrawn, at.UnixMilli(), ask.ID, AskOpen); err != nil {
			return err
		}
	}
	for _, problem := range problems {
		id, err := newAskID()
		if err != nil {
			return err
		}
		questions, err := json.Marshal([]AskQuestion{problem.question})
		if err != nil {
			return err
		}
		route, err := json.Marshal([]AskHop{{To: problem.routedTo, At: at, Why: AskRaised}})
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO asks(id,session_id,origin,title,questions_json,routed_to,routed_at_ms,route_json,state,created_at_ms,ref) VALUES(?,?,?,?,?,?,?,?,?,?,?)`,
			id, problem.sessionID, AskOriginDelivery, problem.title, string(questions), problem.routedTo, at.UnixMilli(), string(route), AskOpen, at.UnixMilli(), problem.ref); err != nil {
			return err
		}
	}
	return nil
}

type deliveryAskLinks struct {
	queue, intent map[int64]string
}

func (r *Registry) deliveryAskLinks() (deliveryAskLinks, error) {
	links := deliveryAskLinks{queue: map[int64]string{}, intent: map[int64]string{}}
	rows, err := r.read.Query(`SELECT a.id,a.ref,COALESCE((SELECT di.queue_id FROM delivery_intents di WHERE a.ref LIKE 'intent:%' AND di.id=CAST(substr(a.ref,8,instr(substr(a.ref,8),':')-1) AS INTEGER)),0)
		FROM asks a WHERE a.origin=? AND a.state=? ORDER BY a.created_at_ms, a.id`, AskOriginDelivery, AskOpen)
	if err != nil {
		return links, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, ref string
		var queueID int64
		if err := rows.Scan(&id, &ref, &queueID); err != nil {
			return links, err
		}
		parsed, ok := ParseDeliveryRef(ref)
		switch {
		case !ok:
		case parsed.QueueID > 0:
			links.queue[parsed.QueueID] = id
		default:
			links.intent[parsed.IntentID] = id
			if queueID > 0 {
				links.queue[queueID] = id
			}
		}
	}
	return links, rows.Err()
}

func (r *Registry) linkQueueAsks(rows []QueueRow) ([]QueueRow, error) {
	links, err := r.deliveryAskLinks()
	if err != nil {
		return nil, err
	}
	for i := range rows {
		rows[i].AskID = links.queue[rows[i].ID]
	}
	return rows, nil
}

func (r *Registry) UntoldDeliveryAsks() ([]Ask, error) {
	return queryAsks(context.Background(), r.read, `WHERE origin=? AND state=? AND announced_at_ms=0 ORDER BY created_at_ms, id`, AskOriginDelivery, AskOpen)
}

func (r *Registry) DeliveryAskOf(intentID int64) (Ask, bool, error) {
	asks, err := queryAsks(context.Background(), r.read, `WHERE origin=? AND state=? AND ref LIKE ? ORDER BY created_at_ms DESC LIMIT 1`, AskOriginDelivery, AskOpen, fmt.Sprintf("intent:%d:%%", intentID))
	if err != nil || len(asks) == 0 {
		return Ask{}, false, err
	}
	return asks[0], true, nil
}

package registry

import (
	"database/sql"
	"errors"
	"fmt"
)

type NotificationWithdraw struct {
	AskID string
	Seq   int64
}

const queueWithdraw = `INSERT INTO notification_withdrawals(ask_id, seq) VALUES (%s, (SELECT COALESCE(MAX(seq),0)+1 FROM notification_withdrawals)) ON CONFLICT(ask_id) DO UPDATE SET seq=excluded.seq`

const shownToPerson = `(OLD.announced_at_ms>0 OR EXISTS(SELECT 1 FROM json_each(OLD.route_json) WHERE json_extract(value,'$.to')='` + AskToPerson + `'))`

// Closing an ask the person may have been shown queues its withdraw in the
// same write, whichever path closes it.
var withdrawTriggers = []struct{ name, on, when, id string }{
	{"asks_closed_withdraw", "UPDATE OF state", `OLD.state='open' AND NEW.state IN ('withdrawn','expired')`, "NEW.id"},
	{"asks_applied_withdraw", "UPDATE OF applied_at_ms", `OLD.applied_at_ms=0 AND NEW.applied_at_ms>0`, "NEW.id"},
	{"asks_deleted_withdraw", "DELETE", `OLD.state='open'`, "OLD.id"},
}

func createWithdrawTriggers(tx *sql.Tx) error {
	for _, trigger := range withdrawTriggers {
		statement := fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON asks\nWHEN %s AND %s\nBEGIN\n\t%s;\nEND", trigger.name, trigger.on, trigger.when, shownToPerson, fmt.Sprintf(queueWithdraw, trigger.id))
		if _, err := tx.Exec(statement); err != nil {
			return err
		}
	}
	return nil
}

func dropWithdrawTriggers(tx *sql.Tx) error {
	for _, trigger := range withdrawTriggers {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS ` + trigger.name); err != nil {
			return err
		}
	}
	return nil
}

// QueueNotificationWithdraw is for a notification this host showed for an
// ask it has no row of, as a paired host's.
func (r *Registry) QueueNotificationWithdraw(askID string) error {
	_, err := r.db.Exec(fmt.Sprintf(queueWithdraw, "?"), askID)
	return err
}

func (r *Registry) PendingNotificationWithdraws() ([]NotificationWithdraw, error) {
	rows, err := r.read.Query(`SELECT ask_id, seq FROM notification_withdrawals ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pending []NotificationWithdraw
	for rows.Next() {
		var w NotificationWithdraw
		if err := rows.Scan(&w.AskID, &w.Seq); err != nil {
			return nil, err
		}
		pending = append(pending, w)
	}
	return pending, rows.Err()
}

// OwedNotificationWithdraw reads the withdraw still owed for askID, which a
// drain that read it earlier may have sent and cleared since.
func (r *Registry) OwedNotificationWithdraw(askID string) (NotificationWithdraw, bool, error) {
	w := NotificationWithdraw{AskID: askID}
	err := r.db.QueryRow(`SELECT seq FROM notification_withdrawals WHERE ask_id=?`, askID).Scan(&w.Seq)
	if errors.Is(err, sql.ErrNoRows) {
		return w, false, nil
	}
	return w, err == nil, err
}

// ClearNotificationWithdraw reports false when w was queued again after it
// was read, so it is still owed.
func (r *Registry) ClearNotificationWithdraw(w NotificationWithdraw) (bool, error) {
	result, err := r.db.Exec(`DELETE FROM notification_withdrawals WHERE ask_id=? AND seq=?`, w.AskID, w.Seq)
	if err != nil {
		return false, err
	}
	cleared, err := result.RowsAffected()
	return cleared == 1, err
}

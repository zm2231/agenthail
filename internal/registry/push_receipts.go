package registry

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

const pushSendsKept = 50

// Recorded before the relay answers, since the phone's receipt can arrive first.
func (r *Registry) RecordPushSent(deviceID, pushID, eventType string, at time.Time) error {
	_, err := r.db.Exec(`INSERT OR IGNORE INTO push_sends (push_id,device_id,event_type,sent_at)
		SELECT ?,id,?,? FROM paired_devices WHERE id=?`, pushID, eventType, formatTimestamp(at), strings.TrimSpace(deviceID))
	return err
}

// Only a confirmed send evicts history; one later withdrawn must not cost a record.
func (r *Registry) SettlePushSent(pushID string, passedOn bool, now time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var deviceID string
	if err := tx.QueryRow(`SELECT device_id FROM push_sends WHERE push_id=?`, pushID).Scan(&deviceID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM push_sends WHERE device_id=? AND confirmed=0 AND push_id!=? AND julianday(sent_at)<julianday(?)`,
		deviceID, pushID, formatTimestamp(now.Add(-24*time.Hour))); err != nil {
		return err
	}
	if !passedOn {
		if _, err := tx.Exec(`DELETE FROM push_sends WHERE push_id=? AND received_at=''`, pushID); err != nil {
			return err
		}
		return tx.Commit()
	}
	if _, err := tx.Exec(`UPDATE push_sends SET confirmed=1 WHERE push_id=?`, pushID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM push_sends WHERE device_id=? AND confirmed=1 AND push_id NOT IN
		(SELECT push_id FROM push_sends WHERE device_id=? AND confirmed=1 ORDER BY julianday(sent_at) DESC LIMIT ?)`, deviceID, deviceID, pushSendsKept); err != nil {
		return err
	}
	return tx.Commit()
}

// A receipt counts only for a push this device was sent, so one device cannot
// mark another's notifications as arrived.
func (r *Registry) RecordPushReceipt(deviceID, pushID string, receivedAt time.Time, alerts string) (bool, error) {
	deviceID = strings.TrimSpace(deviceID)
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	stamp := formatTimestamp(receivedAt)
	result, err := tx.Exec(`UPDATE push_sends SET received_at=? WHERE push_id=? AND device_id=? AND received_at=''`, stamp, pushID, deviceID)
	if err != nil {
		return false, err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return false, nil
	}
	if _, err := tx.Exec(`INSERT INTO push_outcomes (device_id,received_at,alerts) VALUES (?,?,?)
		ON CONFLICT(device_id) DO UPDATE SET
			alerts=CASE WHEN push_outcomes.received_at='' OR julianday(excluded.received_at)>=julianday(push_outcomes.received_at) THEN excluded.alerts ELSE push_outcomes.alerts END,
			received_at=CASE WHEN push_outcomes.received_at='' OR julianday(excluded.received_at)>julianday(push_outcomes.received_at) THEN excluded.received_at ELSE push_outcomes.received_at END`,
		deviceID, stamp, alerts); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

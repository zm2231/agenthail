package registry

import (
	"database/sql"
	"errors"
	"time"
)

const messageSendersMigration = "2026-11-26-yours-told-apart-from-your-agents"

func init() {
	registerMigration(messageSendersMigration, func(tx *sql.Tx) error {
		_, err := tx.Exec(`INSERT OR IGNORE INTO message_senders(message_id,sender_session_id,recorded_at_ms)
			SELECT uuid,sender_session_id,CAST(COALESCE(julianday(created_at)-2440587.5,0)*86400000 AS INTEGER) FROM delivery_intents WHERE uuid!=''`)
		return err
	})
}

type messageSenderWriter interface {
	Exec(string, ...any) (sql.Result, error)
}

func recordMessageSender(tx messageSenderWriter, messageID, senderSessionID string, at time.Time) error {
	if messageID == "" || senderSessionID == "" {
		return nil
	}
	_, err := tx.Exec(`INSERT OR IGNORE INTO message_senders(message_id,sender_session_id,recorded_at_ms) VALUES(?,?,?)`, messageID, senderSessionID, at.UnixMilli())
	return err
}

// RecordMessageSender records who a message Agenthail hands a provider under
// messageID came from, over what its delivery intent recorded. It is recorded
// before the provider sees the message, so a transcript that names the id
// always finds its sender.
func (r *Registry) RecordMessageSender(messageID, senderSessionID string) error {
	if messageID == "" || senderSessionID == "" {
		return nil
	}
	_, err := r.db.Exec(`INSERT INTO message_senders(message_id,sender_session_id,recorded_at_ms) VALUES(?,?,?)
		ON CONFLICT(message_id) DO UPDATE SET sender_session_id=excluded.sender_session_id`, messageID, senderSessionID, r.now().UnixMilli())
	return err
}

func (r *Registry) MessageSender(messageID string) (sender string, found bool, err error) {
	err = r.read.QueryRow(`SELECT sender_session_id FROM message_senders WHERE message_id=?`, messageID).Scan(&sender)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return sender, err == nil, err
}

// MessageSendersSince is when this host began recording the sender of every
// message it sends to the session: when Agenthail began recording them, or
// when the session last moved here from another host, whichever is later. A
// message the provider recorded before then may have come from Agenthail
// without an id, or under an id another host recorded.
func (r *Registry) MessageSendersSince(session string) (time.Time, error) {
	var at int64
	if err := r.read.QueryRow(`SELECT applied_at_ms FROM schema_migrations WHERE name=?`, messageSendersMigration).Scan(&at); err != nil {
		return time.Time{}, err
	}
	since := time.UnixMilli(at)
	var arrived sql.NullString
	if err := r.read.QueryRow(`SELECT MAX(created_at) FROM session_moves WHERE conversation=? AND role='in' AND state=?`, session, MoveResumed).Scan(&arrived); err != nil {
		return time.Time{}, err
	}
	if !arrived.Valid {
		return since, nil
	}
	moved, err := parseTimestamp(arrived.String)
	if err != nil {
		return time.Time{}, err
	}
	if moved.After(since) {
		return moved, nil
	}
	return since, nil
}

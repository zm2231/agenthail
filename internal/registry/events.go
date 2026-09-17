package registry

import (
	"database/sql"
	"fmt"
	"time"
)

type DaemonEvent struct {
	ID        uint64
	Type      string
	EntityID  string
	Payload   []byte
	CreatedAt time.Time
}

func (r *Registry) AppendDaemonEvent(eventType, entityID string, payload []byte, createdAt time.Time, retain int) (DaemonEvent, error) {
	event, _, err := r.AppendDaemonEventWithKey(eventType, entityID, payload, createdAt, retain, "")
	return event, err
}

func (r *Registry) AppendDaemonEventWithKey(eventType, entityID string, payload []byte, createdAt time.Time, retain int, dedupeKey string) (DaemonEvent, bool, error) {
	if eventType == "" || retain < 1 {
		return DaemonEvent{}, false, fmt.Errorf("event type and positive retention are required")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return DaemonEvent{}, false, err
	}
	defer tx.Rollback()
	createdAt = createdAt.UTC()
	if dedupeKey != "" {
		result, err := tx.Exec(`INSERT OR IGNORE INTO daemon_event_items (dedupe_key,event_id,created_at) VALUES (?,?,?)`, dedupeKey, 0, createdAt.Format(time.RFC3339Nano))
		if err != nil {
			return DaemonEvent{}, false, err
		}
		created, err := result.RowsAffected()
		if err != nil {
			return DaemonEvent{}, false, err
		}
		if created == 0 {
			return DaemonEvent{}, false, nil
		}
	}
	result, err := tx.Exec(`INSERT OR IGNORE INTO daemon_events (event_type,entity_id,payload,created_at,dedupe_key) VALUES (?,?,?,?,?)`, eventType, entityID, payload, createdAt.Format(time.RFC3339Nano), dedupeKey)
	if err != nil {
		return DaemonEvent{}, false, err
	}
	created, err := result.RowsAffected()
	if err != nil {
		return DaemonEvent{}, false, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return DaemonEvent{}, false, err
	}
	if created == 0 && dedupeKey != "" {
		return DaemonEvent{}, false, nil
	}
	if dedupeKey != "" {
		if _, err := tx.Exec(`UPDATE daemon_event_items SET event_id=? WHERE dedupe_key=?`, id, dedupeKey); err != nil {
			return DaemonEvent{}, false, err
		}
	}
	if _, err := tx.Exec(`DELETE FROM daemon_events WHERE id NOT IN (SELECT id FROM daemon_events ORDER BY id DESC LIMIT ?)`, retain); err != nil {
		return DaemonEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return DaemonEvent{}, false, err
	}
	return DaemonEvent{ID: uint64(id), Type: eventType, EntityID: entityID, Payload: append([]byte(nil), payload...), CreatedAt: createdAt}, true, nil
}

func (r *Registry) HasDaemonEventItem(dedupeKey string) (bool, error) {
	var exists int
	err := r.db.QueryRow(`SELECT 1 FROM daemon_event_items WHERE dedupe_key=?`, dedupeKey).Scan(&exists)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil && exists == 1, err
}

func (r *Registry) RecentDaemonEvents(limit int) ([]DaemonEvent, error) {
	if limit < 1 {
		return []DaemonEvent{}, nil
	}
	rows, err := r.db.Query(`SELECT id,event_type,entity_id,payload,created_at FROM (SELECT id,event_type,entity_id,payload,created_at FROM daemon_events ORDER BY id DESC LIMIT ?) ORDER BY id`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := []DaemonEvent{}
	for rows.Next() {
		var event DaemonEvent
		var id int64
		var createdAt string
		if err := rows.Scan(&id, &event.Type, &event.EntityID, &event.Payload, &createdAt); err != nil {
			return nil, err
		}
		if id < 0 {
			return nil, fmt.Errorf("invalid daemon event id %d", id)
		}
		event.ID = uint64(id)
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return events, nil
}

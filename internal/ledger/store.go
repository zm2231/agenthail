package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Store struct {
	db   *sql.DB
	read *sql.DB
}

func NewStore(db, read *sql.DB) *Store {
	return &Store{db: db, read: read}
}

func (s *Store) Enabled(projectKey string) (bool, error) {
	if projectKey == "" {
		return false, nil
	}
	var enabled int
	err := s.read.QueryRow(`SELECT enabled FROM ledger_state WHERE project_key=?`, projectKey).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return enabled == 1, nil
}

func (s *Store) Enable(projectKey string) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	_, err := s.db.Exec(`INSERT INTO ledger_state(project_key,enabled) VALUES(?,1) ON CONFLICT(project_key) DO UPDATE SET enabled=1`, projectKey)
	return err
}

func (s *Store) Disable(projectKey string) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	_, err := s.db.Exec(`UPDATE ledger_state SET enabled=0 WHERE project_key=?`, projectKey)
	return err
}

func (s *Store) AppendEvent(event Event) (Event, error) {
	if event.ProjectKey == "" || event.Type == "" {
		return Event{}, fmt.Errorf("project key and event type are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	result, err := s.db.Exec(
		`INSERT INTO ledger_events(project_key,type,payload,actor_session_id,actor_host_id,owner_seq,created_at) VALUES(?,?,?,?,?,?,?)`,
		event.ProjectKey, string(event.Type), event.Payload, event.ActorSessionID, event.ActorHostID, event.OwnerSeq,
		event.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return Event{}, err
	}
	seq, err := result.LastInsertId()
	if err != nil {
		return Event{}, err
	}
	event.Seq = seq
	return event, nil
}

func (s *Store) Events(projectKey string, afterSeq int64, limit int) ([]Event, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.read.Query(
		`SELECT seq,project_key,type,payload,actor_session_id,actor_host_id,owner_seq,created_at FROM ledger_events WHERE project_key=? AND seq>? ORDER BY seq LIMIT ?`,
		projectKey, afterSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var typ, ts string
		if err := rows.Scan(&e.Seq, &e.ProjectKey, &typ, &e.Payload, &e.ActorSessionID, &e.ActorHostID, &e.OwnerSeq, &ts); err != nil {
			return nil, err
		}
		e.Type = EventType(typ)
		e.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", ts)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) RecordUtterance(projectKey, messageID, sessionID, surface, text string) (Event, error) {
	payload := UtterancePayload{
		Text:      text,
		MessageID: messageID,
		SessionID: sessionID,
		Surface:   surface,
		TS:        time.Now().UTC().Format(time.RFC3339),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return Event{}, err
	}
	return s.AppendEvent(Event{
		ProjectKey:     projectKey,
		Type:           EventUtterance,
		Payload:        data,
		ActorSessionID: sessionID,
	})
}

func (s *Store) LatestSeq(projectKey string) (int64, error) {
	var seq sql.NullInt64
	err := s.read.QueryRow(`SELECT MAX(seq) FROM ledger_events WHERE project_key=?`, projectKey).Scan(&seq)
	if err != nil {
		return 0, err
	}
	if !seq.Valid {
		return 0, nil
	}
	return seq.Int64, nil
}

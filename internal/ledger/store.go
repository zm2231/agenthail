package ledger

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Store struct {
	db    *sql.DB
	read  *sql.DB
	cache *boardCache
}

func NewStore(db, read *sql.DB) *Store {
	return &Store{db: db, read: read}
}

const (
	LedgerUnset    = 0
	LedgerEnabled  = 1
	LedgerDisabled = 2
)

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
	return enabled == LedgerEnabled, nil
}

func (s *Store) AnyEnabled() (bool, error) {
	var one int
	err := s.read.QueryRow(`SELECT 1 FROM ledger_state WHERE enabled=? LIMIT 1`, LedgerEnabled).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

type Scope struct {
	State int
	Since time.Time
}

func (s *Store) Scope(projectKey string) (Scope, error) {
	var state int
	var since int64
	err := s.read.QueryRow(`SELECT enabled,since_ms FROM ledger_state WHERE project_key=?`, projectKey).Scan(&state, &since)
	if errors.Is(err, sql.ErrNoRows) {
		return Scope{State: LedgerUnset}, nil
	}
	if err != nil {
		return Scope{}, err
	}
	return Scope{State: state, Since: time.UnixMilli(since).UTC()}, nil
}

func (s *Store) Enable(projectKey string) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	_, err := s.db.Exec(`INSERT INTO ledger_state(project_key,enabled,since_ms) VALUES(?,?,?)
		ON CONFLICT(project_key) DO UPDATE SET since_ms=CASE WHEN enabled=excluded.enabled THEN since_ms ELSE excluded.since_ms END, enabled=excluded.enabled`,
		projectKey, LedgerEnabled, time.Now().UnixMilli())
	return err
}

func (s *Store) Disable(projectKey string) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	_, err := s.db.Exec(`INSERT INTO ledger_state(project_key,enabled) VALUES(?,?) ON CONFLICT(project_key) DO UPDATE SET enabled=?`, projectKey, LedgerDisabled, LedgerDisabled)
	return err
}

func (s *Store) AppendEvent(event Event) (Event, error) {
	if event.ProjectKey == "" || event.Type == "" {
		return Event{}, fmt.Errorf("project key and event type are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	return insertEvent(s.db, `INSERT`, event)
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

const eventColumns = `seq,project_key,type,payload,actor_session_id,actor_host_id,owner_seq,ref,created_at`

// insertEvent returns Seq 0 when verb is INSERT OR IGNORE and the ref already exists.
func insertEvent(db execer, verb string, event Event) (Event, error) {
	result, err := db.Exec(
		verb+` INTO ledger_events(project_key,type,payload,actor_session_id,actor_host_id,owner_seq,ref,created_at) VALUES(?,?,?,?,?,?,?,?)`,
		event.ProjectKey, string(event.Type), event.Payload, event.ActorSessionID, event.ActorHostID, event.OwnerSeq,
		event.Ref, event.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	)
	if err != nil {
		return Event{}, err
	}
	if n, err := result.RowsAffected(); err != nil || n == 0 {
		return Event{}, err
	}
	if event.Seq, err = result.LastInsertId(); err != nil {
		return Event{}, err
	}
	return event, nil
}

func scanEvent(row rowScanner) (Event, error) {
	var e Event
	var typ, ts string
	if err := row.Scan(&e.Seq, &e.ProjectKey, &typ, &e.Payload, &e.ActorSessionID, &e.ActorHostID, &e.OwnerSeq, &e.Ref, &ts); err != nil {
		return Event{}, err
	}
	e.Type = EventType(typ)
	e.CreatedAt, _ = time.Parse("2006-01-02T15:04:05Z", ts)
	return e, nil
}

func (s *Store) Events(projectKey string, afterSeq int64, limit int) ([]Event, error) {
	if limit < 1 {
		limit = 100
	}
	rows, err := s.read.Query(
		`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND seq>? ORDER BY seq LIMIT ?`,
		projectKey, afterSeq, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
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

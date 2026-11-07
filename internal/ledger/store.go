package ledger

import (
	"database/sql"
	"fmt"
	"sync/atomic"
	"time"
)

type Store struct {
	db       *sql.DB
	read     *sql.DB
	cache    *boardCache
	changed  atomic.Pointer[func(string)]
	recorded atomic.Pointer[func(string)]
}

func NewStore(db, read *sql.DB) *Store {
	return &Store{db: db, read: read}
}

func (s *Store) AppendEvent(event Event) (Event, error) {
	if event.ProjectKey == "" || event.Type == "" {
		return Event{}, fmt.Errorf("project key and event type are required")
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	inserted, err := insertEvent(s.db, `INSERT`, event)
	if err == nil {
		s.notify(event.ProjectKey)
		s.notifyRecorded(event.ProjectKey)
	}
	return inserted, err
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type rowScanner interface {
	Scan(dest ...any) error
}

const eventColumns = `seq,project_key,type,payload,actor_session_id,actor_host_id,owner_seq,origin_seq,ref,created_at`

const MaxEventBytes = 4 << 20

// insertEvent returns Seq 0 when verb is INSERT OR IGNORE and the ref or origin already exists.
func insertEvent(db execer, verb string, event Event) (Event, error) {
	if len(event.Payload) > MaxEventBytes {
		return Event{}, refuse("a ledger event holds at most %d bytes; this %s holds %d", MaxEventBytes, event.Type, len(event.Payload))
	}
	if event.Stamp == "" {
		event.Stamp = event.CreatedAt.UTC().Format(eventTimeLayout)
	}
	result, err := db.Exec(
		verb+` INTO ledger_events(project_key,type,payload,actor_session_id,actor_host_id,owner_seq,origin_seq,ref,created_at) VALUES(?,?,?,?,?,?,?,?,?)`,
		event.ProjectKey, string(event.Type), event.Payload, event.ActorSessionID, event.ActorHostID, event.OwnerSeq, event.OriginSeq,
		event.Ref, event.Stamp,
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
	if event.OriginSeq == 0 {
		if _, err := db.Exec(`UPDATE ledger_events SET actor_host_id=COALESCE((SELECT host_id FROM host_identity WHERE id=1),''), origin_seq=seq WHERE seq=?`, event.Seq); err != nil {
			return Event{}, err
		}
		event.OriginSeq = event.Seq
	}
	return event, nil
}

func scanEvent(row rowScanner) (Event, error) {
	var e Event
	var typ, ts string
	if err := row.Scan(&e.Seq, &e.ProjectKey, &typ, &e.Payload, &e.ActorSessionID, &e.ActorHostID, &e.OwnerSeq, &e.OriginSeq, &e.Ref, &ts); err != nil {
		return Event{}, err
	}
	e.Type = EventType(typ)
	e.Stamp = ts
	e.CreatedAt, _ = time.Parse(eventTimeLayout, ts)
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

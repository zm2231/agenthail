package registry

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"strings"
	"time"
)

const catalogEventRetention = 4096

type CatalogEvent struct {
	Seq       uint64
	DedupeKey string
	Type      string
	EntityID  string
	Payload   []byte
	CreatedAt time.Time
}

type CatalogEventWindow struct {
	Events      []CatalogEvent
	HostEpoch   string
	EarliestSeq uint64
	LatestSeq   uint64
	Gap         bool
}

func (r *Registry) EnsureCatalogState() error {
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_sessions (
		session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE,
		host_project BLOB NOT NULL,
		checkout BLOB NOT NULL,
		unavailable_reason TEXT NOT NULL DEFAULT '',
		observed_at TEXT NOT NULL,
		misses INTEGER NOT NULL DEFAULT 0,
		projection_fingerprint TEXT NOT NULL DEFAULT '',
		projection_generation INTEGER NOT NULL DEFAULT 0,
		discovery_failures INTEGER NOT NULL DEFAULT 0
	)`); err != nil {
		return err
	}
	if err := r.ensureColumn("catalog_sessions", "misses", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := r.ensureColumn("catalog_sessions", "projection_fingerprint", `TEXT NOT NULL DEFAULT ''`); err != nil {
		return err
	}
	if err := r.ensureColumn("catalog_sessions", "projection_generation", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if err := r.ensureColumn("catalog_sessions", "discovery_failures", `INTEGER NOT NULL DEFAULT 0`); err != nil {
		return err
	}
	if _, err := r.db.Exec(`CREATE TABLE IF NOT EXISTS catalog_surfaces (
		surface TEXT PRIMARY KEY,
		health TEXT NOT NULL,
		detail TEXT NOT NULL DEFAULT '',
		observed_at TEXT NOT NULL
	)`); err != nil {
		return err
	}
	_, err := r.catalogHostEpoch(true)
	return err
}

func (r *Registry) CatalogState() (string, uint64, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", 0, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return "", 0, err
	}
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM catalog_events`).Scan(&latest); err != nil {
		return "", 0, err
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	if !latest.Valid {
		return epoch, 0, nil
	}
	if latest.Int64 < 0 {
		return "", 0, fmt.Errorf("invalid catalog sequence")
	}
	return epoch, uint64(latest.Int64), nil
}

func (r *Registry) AppendCatalogEvent(input CatalogEvent) (CatalogEvent, bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	event, created, err := r.AppendCatalogEventTx(tx, input)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogEvent{}, false, err
	}
	return event, created, nil
}

func (r *Registry) AppendCatalogEventTx(tx *sql.Tx, input CatalogEvent) (CatalogEvent, bool, error) {
	return appendCatalogEventTx(tx, input)
}

func appendCatalogEventTx(tx *sql.Tx, input CatalogEvent) (CatalogEvent, bool, error) {
	if tx == nil {
		return CatalogEvent{}, false, fmt.Errorf("catalog event transaction is required")
	}
	if strings.TrimSpace(input.DedupeKey) == "" || strings.TrimSpace(input.Type) == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog event requires dedupe key and type")
	}
	if input.CreatedAt.IsZero() {
		input.CreatedAt = time.Now().UTC()
	} else {
		input.CreatedAt = input.CreatedAt.UTC()
	}
	input.Payload = append([]byte(nil), input.Payload...)
	if _, err := catalogHostEpochTx(tx, true); err != nil {
		return CatalogEvent{}, false, err
	}
	if existing, err := catalogEventByDedupeKey(tx, input.DedupeKey); err == nil {
		return existing, false, nil
	} else if err != sql.ErrNoRows {
		return CatalogEvent{}, false, err
	}
	result, err := tx.Exec(`INSERT INTO catalog_events(dedupe_key,type,entity_id,payload,created_at) VALUES(?,?,?,?,?)`, input.DedupeKey, input.Type, input.EntityID, input.Payload, input.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return CatalogEvent{}, false, err
	}
	sequence, err := result.LastInsertId()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	input.Seq = uint64(sequence)
	if _, err := tx.Exec(`DELETE FROM catalog_events WHERE seq NOT IN (SELECT seq FROM catalog_events ORDER BY seq DESC LIMIT ?)`, catalogEventRetention); err != nil {
		return CatalogEvent{}, false, err
	}
	return input, true, nil
}

func (r *Registry) CatalogEventsAfter(after uint64, limit int) (CatalogEventWindow, error) {
	if limit < 1 {
		return CatalogEventWindow{Events: []CatalogEvent{}}, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEventWindow{}, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return CatalogEventWindow{}, err
	}
	var earliest, latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MIN(seq),MAX(seq) FROM catalog_events`).Scan(&earliest, &latest); err != nil {
		return CatalogEventWindow{}, err
	}
	window := CatalogEventWindow{Events: []CatalogEvent{}, HostEpoch: epoch}
	if !earliest.Valid || !latest.Valid {
		if err := tx.Commit(); err != nil {
			return CatalogEventWindow{}, err
		}
		return window, nil
	}
	window.EarliestSeq = uint64(earliest.Int64)
	window.LatestSeq = uint64(latest.Int64)
	window.Gap = after < window.EarliestSeq-1 || after > window.LatestSeq
	if window.Gap {
		if err := tx.Commit(); err != nil {
			return CatalogEventWindow{}, err
		}
		return window, nil
	}
	rows, err := tx.Query(`SELECT seq,dedupe_key,type,entity_id,payload,created_at FROM catalog_events WHERE seq>? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return CatalogEventWindow{}, err
	}
	defer rows.Close()
	for rows.Next() {
		entry, err := scanCatalogEvent(rows)
		if err != nil {
			return CatalogEventWindow{}, err
		}
		window.Events = append(window.Events, entry)
	}
	if err := rows.Err(); err != nil {
		return CatalogEventWindow{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogEventWindow{}, err
	}
	return window, nil
}

func (r *Registry) BeginSessionJournalSource(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", fmt.Errorf("session id is required")
	}
	epoch, err := catalogRandomID()
	if err != nil {
		return "", err
	}
	result, err := r.db.Exec(`UPDATE session_journal_state SET source_epoch=? WHERE session_id=?`, epoch, sessionID)
	if err != nil {
		return "", err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if n == 0 {
		if _, err := r.db.Exec(`INSERT INTO session_journal_state(session_id,next_seq,retained_bytes,source_epoch) VALUES(?,0,0,?)`, sessionID, epoch); err != nil {
			return "", err
		}
	}
	return epoch, nil
}

func (r *Registry) catalogHostEpoch(create bool) (string, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, create)
	if err != nil {
		return "", err
	}
	return epoch, tx.Commit()
}

func catalogHostEpochTx(tx *sql.Tx, create bool) (string, error) {
	var epoch string
	err := tx.QueryRow(`SELECT host_epoch FROM catalog_state WHERE id=1`).Scan(&epoch)
	if err == nil {
		return epoch, nil
	}
	if err != sql.ErrNoRows || !create {
		return "", err
	}
	epoch, err = catalogRandomID()
	if err != nil {
		return "", err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_state(id,host_epoch) VALUES(1,?)`, epoch); err != nil {
		return "", err
	}
	return epoch, nil
}

func catalogEventByDedupeKey(q interface{ QueryRow(string, ...any) *sql.Row }, key string) (CatalogEvent, error) {
	return scanCatalogEvent(q.QueryRow(`SELECT seq,dedupe_key,type,entity_id,payload,created_at FROM catalog_events WHERE dedupe_key=?`, key))
}

func scanCatalogEvent(row interface{ Scan(...any) error }) (CatalogEvent, error) {
	var event CatalogEvent
	var sequence int64
	var createdAt string
	if err := row.Scan(&sequence, &event.DedupeKey, &event.Type, &event.EntityID, &event.Payload, &createdAt); err != nil {
		return CatalogEvent{}, err
	}
	if sequence < 1 {
		return CatalogEvent{}, fmt.Errorf("invalid catalog sequence %d", sequence)
	}
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return CatalogEvent{}, fmt.Errorf("parse catalog event timestamp: %w", err)
	}
	event.Seq = uint64(sequence)
	event.CreatedAt = parsed
	event.Payload = append([]byte(nil), event.Payload...)
	return event, nil
}

func catalogRandomID() (string, error) {
	buffer := make([]byte, 16)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

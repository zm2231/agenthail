package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type CatalogSessionState struct {
	Session           surface.Session
	HostProject       json.RawMessage
	Checkout          json.RawMessage
	UnavailableReason string
	ObservedAt        time.Time
}

type CatalogSnapshot struct {
	HostEpoch  string
	CatalogSeq uint64
	Sessions   []CatalogSessionState
}

func (r *Registry) RecordCatalogSession(state CatalogSessionState, event CatalogEvent) (CatalogEvent, bool, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return CatalogEvent{}, false, err
	}
	if state.Session.ID == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog session id is required")
	}
	if !json.Valid(state.HostProject) || !json.Valid(state.Checkout) {
		return CatalogEvent{}, false, fmt.Errorf("catalog identity must be JSON")
	}
	if state.ObservedAt.IsZero() {
		state.ObservedAt = time.Now().UTC()
	} else {
		state.ObservedAt = state.ObservedAt.UTC()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	if err := registerSessionTx(tx, state.Session); err != nil {
		return CatalogEvent{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,unavailable_reason,observed_at)
		VALUES(?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET host_project=excluded.host_project,
		checkout=excluded.checkout, unavailable_reason=excluded.unavailable_reason, observed_at=excluded.observed_at`,
		state.Session.ID, []byte(state.HostProject), []byte(state.Checkout), state.UnavailableReason, state.ObservedAt.Format(time.RFC3339Nano)); err != nil {
		return CatalogEvent{}, false, err
	}
	persisted, created, err := r.AppendCatalogEventTx(tx, event)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogEvent{}, false, err
	}
	return persisted, created, nil
}

func (r *Registry) CatalogSnapshot() (CatalogSnapshot, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM catalog_events`).Scan(&latest); err != nil {
		return CatalogSnapshot{}, err
	}
	rows, err := tx.Query(`SELECT s.id,s.surface,s.name,s.cwd,s.pid,s.status,s.transcript,s.has_local,s.source,s.transport,s.last_active_ms,
		cs.host_project,cs.checkout,cs.unavailable_reason,cs.observed_at
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		ORDER BY s.last_active_ms DESC,s.updated_at DESC,s.id`)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer rows.Close()
	snapshot := CatalogSnapshot{HostEpoch: epoch, Sessions: []CatalogSessionState{}}
	if latest.Valid {
		if latest.Int64 < 0 {
			return CatalogSnapshot{}, fmt.Errorf("invalid catalog sequence")
		}
		snapshot.CatalogSeq = uint64(latest.Int64)
	}
	for rows.Next() {
		var state CatalogSessionState
		var kind, status, observedAt string
		var hasLocal int
		var lastActiveMS int64
		if err := rows.Scan(&state.Session.ID, &kind, &state.Session.Name, &state.Session.Cwd, &state.Session.PID, &status, &state.Session.Transcript, &hasLocal, &state.Session.Source, &state.Session.Transport, &lastActiveMS, &state.HostProject, &state.Checkout, &state.UnavailableReason, &observedAt); err != nil {
			return CatalogSnapshot{}, err
		}
		state.Session.Surface = surface.SurfaceKind(kind)
		state.Session.Status = surface.SessionStatus(status)
		state.Session.HasLocal = hasLocal != 0
		if lastActiveMS > 0 {
			state.Session.LastActive = time.UnixMilli(lastActiveMS)
		}
		parsed, err := time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return CatalogSnapshot{}, fmt.Errorf("parse catalog observation: %w", err)
		}
		state.ObservedAt = parsed
		snapshot.Sessions = append(snapshot.Sessions, state)
	}
	if err := rows.Err(); err != nil {
		return CatalogSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogSnapshot{}, err
	}
	return snapshot, nil
}

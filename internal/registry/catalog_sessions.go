package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type CatalogSessionState struct {
	Session               surface.Session
	HostProject           json.RawMessage
	Checkout              json.RawMessage
	UnavailableReason     string
	ObservedAt            time.Time
	ProjectionFingerprint string
}

type CatalogSnapshot struct {
	HostEpoch  string
	CatalogSeq uint64
	Sessions   []CatalogSessionState
	Surfaces   []CatalogSurfaceState
}

type CatalogSurfaceState struct {
	Surface    surface.SurfaceKind
	Health     string
	Detail     string
	ObservedAt time.Time
}

func (r *Registry) RecordCatalogSurface(state CatalogSurfaceState, event CatalogEvent) (CatalogEvent, bool, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return CatalogEvent{}, false, err
	}
	if state.Surface == "" || state.Health == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog surface and health are required")
	}
	if state.ObservedAt.IsZero() {
		state.ObservedAt = time.Now().UTC()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO catalog_surfaces(surface,health,detail,observed_at) VALUES(?,?,?,?) ON CONFLICT(surface) DO UPDATE SET health=excluded.health,detail=excluded.detail,observed_at=excluded.observed_at`, string(state.Surface), state.Health, state.Detail, state.ObservedAt.UTC().Format(time.RFC3339Nano)); err != nil {
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

func (r *Registry) RecordCatalogSession(state CatalogSessionState, event CatalogEvent) (CatalogEvent, bool, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return CatalogEvent{}, false, err
	}
	if state.Session.ID == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog session id is required")
	}
	if state.ProjectionFingerprint == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog session projection fingerprint is required")
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
	var priorFingerprint string
	var priorGeneration int64
	err = tx.QueryRow(`SELECT projection_fingerprint,projection_generation FROM catalog_sessions WHERE session_id=?`, state.Session.ID).Scan(&priorFingerprint, &priorGeneration)
	if err != nil && err != sql.ErrNoRows {
		return CatalogEvent{}, false, err
	}
	changed := err == sql.ErrNoRows || priorFingerprint != state.ProjectionFingerprint
	if changed {
		priorGeneration++
	}
	if err := registerSessionTx(tx, state.Session); err != nil {
		return CatalogEvent{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,unavailable_reason,observed_at,projection_fingerprint,projection_generation)
		VALUES(?,?,?,?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET host_project=excluded.host_project,
		checkout=excluded.checkout, unavailable_reason=excluded.unavailable_reason, observed_at=excluded.observed_at, misses=0,
		projection_fingerprint=excluded.projection_fingerprint, projection_generation=excluded.projection_generation`,
		state.Session.ID, []byte(state.HostProject), []byte(state.Checkout), state.UnavailableReason, state.ObservedAt.Format(time.RFC3339Nano), state.ProjectionFingerprint, priorGeneration); err != nil {
		return CatalogEvent{}, false, err
	}
	if !changed {
		if err := tx.Commit(); err != nil {
			return CatalogEvent{}, false, err
		}
		return CatalogEvent{}, false, nil
	}
	event.DedupeKey = fmt.Sprintf("%s:%d", event.DedupeKey, priorGeneration)
	persisted, created, err := r.AppendCatalogEventTx(tx, event)
	if err != nil {
		return CatalogEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogEvent{}, false, err
	}
	return persisted, created, nil
}

func (r *Registry) ReconcileCatalogOmissions(kind surface.SurfaceKind, seen map[string]struct{}, threshold int) ([]CatalogEvent, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return nil, err
	}
	if threshold < 1 {
		return nil, fmt.Errorf("catalog omission threshold must be positive")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT cs.session_id,cs.observed_at,cs.misses FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.surface=?`, string(kind))
	if err != nil {
		return nil, err
	}
	deferred := []struct {
		id, observedAt string
		misses         int
	}{}
	for rows.Next() {
		var item struct {
			id, observedAt string
			misses         int
		}
		if err := rows.Scan(&item.id, &item.observedAt, &item.misses); err != nil {
			rows.Close()
			return nil, err
		}
		if _, present := seen[item.id]; !present {
			deferred = append(deferred, item)
		}
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	events := []CatalogEvent{}
	for _, item := range deferred {
		misses := item.misses + 1
		if misses < threshold {
			if _, err := tx.Exec(`UPDATE catalog_sessions SET misses=? WHERE session_id=?`, misses, item.id); err != nil {
				return nil, err
			}
			continue
		}
		payload, err := json.Marshal(map[string]string{"sessionId": item.id, "surface": string(kind), "reason": "omitted_from_successful_discovery"})
		if err != nil {
			return nil, err
		}
		event, created, err := r.AppendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + item.id + ":" + item.observedAt, Type: "session.removed", EntityID: item.id, Payload: payload})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, item.id); err != nil {
			return nil, err
		}
		if created {
			events = append(events, event)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return events, nil
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
	rows, err := tx.Query(`SELECT s.id,s.surface,s.name,s.cwd,s.pid,s.status,s.transcript,s.has_local,s.source,s.transport,s.configured_model,s.last_active_ms,
		cs.host_project,cs.checkout,cs.unavailable_reason,cs.observed_at,cs.projection_fingerprint
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		ORDER BY s.last_active_ms DESC,s.updated_at DESC,s.id`)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer rows.Close()
	snapshot := CatalogSnapshot{HostEpoch: epoch, Sessions: []CatalogSessionState{}, Surfaces: []CatalogSurfaceState{}}
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
		if err := rows.Scan(&state.Session.ID, &kind, &state.Session.Name, &state.Session.Cwd, &state.Session.PID, &status, &state.Session.Transcript, &hasLocal, &state.Session.Source, &state.Session.Transport, &state.Session.ConfiguredModel, &lastActiveMS, &state.HostProject, &state.Checkout, &state.UnavailableReason, &observedAt, &state.ProjectionFingerprint); err != nil {
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
	surfaceRows, err := tx.Query(`SELECT surface,health,detail,observed_at FROM catalog_surfaces ORDER BY surface`)
	if err != nil {
		return CatalogSnapshot{}, err
	}
	defer surfaceRows.Close()
	for surfaceRows.Next() {
		var state CatalogSurfaceState
		var observedAt string
		if err := surfaceRows.Scan(&state.Surface, &state.Health, &state.Detail, &observedAt); err != nil {
			return CatalogSnapshot{}, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return CatalogSnapshot{}, fmt.Errorf("parse catalog surface observation: %w", err)
		}
		state.ObservedAt = parsed
		snapshot.Surfaces = append(snapshot.Surfaces, state)
	}
	if err := surfaceRows.Err(); err != nil {
		return CatalogSnapshot{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogSnapshot{}, err
	}
	return snapshot, nil
}

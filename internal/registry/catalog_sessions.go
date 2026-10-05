package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
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
	Freshness             CatalogFreshness
}

type CatalogFreshness struct {
	Generation uint64    `json:"generation"`
	ObservedAt time.Time `json:"observedAt"`
	Stale      bool      `json:"stale"`
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

// CatalogQueueProjectionConflict reports that a caller built a projection
// from a queue count that changed before the catalog transaction could commit.
type CatalogQueueProjectionConflict struct {
	SessionID  string
	QueueCount int
}

func (e *CatalogQueueProjectionConflict) Error() string {
	return fmt.Sprintf("catalog queue projection for %s is stale; current queue count is %d", e.SessionID, e.QueueCount)
}

func queueCountInProjection(raw []byte) (int, bool) {
	var projection struct {
		QueueCount *int `json:"queueCount"`
	}
	if err := json.Unmarshal(raw, &projection); err != nil {
		return 0, false
	}
	if projection.QueueCount == nil {
		return 0, false
	}
	return *projection.QueueCount, true
}

func queueCountInCatalogEvent(raw []byte) (int, bool) {
	var envelope struct {
		Session json.RawMessage `json:"session"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return 0, false
	}
	if len(envelope.Session) == 0 {
		return 0, false
	}
	return queueCountInProjection(envelope.Session)
}

func queueCountTx(tx *sql.Tx, sessionID string, now time.Time) (int, error) {
	var count int
	err := tx.QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND status IN ('pending','inflight') AND (status='inflight' OR expires_at_ms=0 OR expires_at_ms>?)`, sessionID, now.UnixMilli()).Scan(&count)
	return count, err
}

func validateQueueProjectionTx(tx *sql.Tx, sessionID string, fingerprint, eventPayload []byte) error {
	count, err := queueCountTx(tx, sessionID, time.Now())
	if err != nil {
		return err
	}
	projected, present := queueCountInProjection(fingerprint)
	if present && projected != count {
		return &CatalogQueueProjectionConflict{SessionID: sessionID, QueueCount: count}
	}
	if len(eventPayload) > 0 {
		projected, present = queueCountInCatalogEvent(eventPayload)
		if present && projected != count {
			return &CatalogQueueProjectionConflict{SessionID: sessionID, QueueCount: count}
		}
	}
	return nil
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
	var priorHealth, priorDetail string
	err = tx.QueryRow(`SELECT health,detail FROM catalog_surfaces WHERE surface=?`, string(state.Surface)).Scan(&priorHealth, &priorDetail)
	if err != nil && err != sql.ErrNoRows {
		return CatalogEvent{}, false, err
	}
	changed := err == sql.ErrNoRows || priorHealth != state.Health || priorDetail != state.Detail
	observedAt := state.ObservedAt.UTC().Format(time.RFC3339Nano)
	if _, err := tx.Exec(`INSERT INTO catalog_surfaces(surface,health,detail,observed_at) VALUES(?,?,?,?) ON CONFLICT(surface) DO UPDATE SET health=excluded.health,detail=excluded.detail,observed_at=excluded.observed_at`, string(state.Surface), state.Health, state.Detail, observedAt); err != nil {
		return CatalogEvent{}, false, err
	}
	if !changed {
		if err := tx.Commit(); err != nil {
			return CatalogEvent{}, false, err
		}
		return CatalogEvent{}, false, nil
	}
	event.DedupeKey = event.DedupeKey + ":" + observedAt
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
	if err := validateQueueProjectionTx(tx, state.Session.ID, []byte(state.ProjectionFingerprint), event.Payload); err != nil {
		return CatalogEvent{}, false, err
	}
	var priorFingerprint, priorUnavailable string
	var priorHostProject, priorCheckout []byte
	var priorGeneration, priorMisses, priorDiscoveryFailures int64
	err = tx.QueryRow(`SELECT projection_fingerprint,projection_generation,misses,discovery_failures,host_project,checkout,unavailable_reason FROM catalog_sessions WHERE session_id=?`, state.Session.ID).Scan(&priorFingerprint, &priorGeneration, &priorMisses, &priorDiscoveryFailures, &priorHostProject, &priorCheckout, &priorUnavailable)
	if err != nil && err != sql.ErrNoRows {
		return CatalogEvent{}, false, err
	}
	changed := err == sql.ErrNoRows || priorFingerprint != state.ProjectionFingerprint || priorMisses > 0 || priorDiscoveryFailures > 0
	if !changed && string(priorHostProject) == string(state.HostProject) && string(priorCheckout) == string(state.Checkout) && priorUnavailable == state.UnavailableReason {
		current, err := sessionRowMatchesTx(tx, state.Session)
		if err != nil {
			return CatalogEvent{}, false, err
		}
		if current {
			return CatalogEvent{}, false, tx.Commit()
		}
	}
	if changed {
		priorGeneration++
	}
	if err := registerSessionTx(tx, state.Session); err != nil {
		return CatalogEvent{}, false, err
	}
	if _, err := tx.Exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,unavailable_reason,observed_at,projection_fingerprint,projection_generation,discovery_failures)
		VALUES(?,?,?,?,?,?,?,0) ON CONFLICT(session_id) DO UPDATE SET host_project=excluded.host_project,
		checkout=excluded.checkout, unavailable_reason=excluded.unavailable_reason, observed_at=excluded.observed_at, misses=0,
		projection_fingerprint=excluded.projection_fingerprint, projection_generation=excluded.projection_generation, discovery_failures=0`,
		state.Session.ID, []byte(state.HostProject), []byte(state.Checkout), state.UnavailableReason, state.ObservedAt.Format(time.RFC3339Nano), state.ProjectionFingerprint, priorGeneration); err != nil {
		return CatalogEvent{}, false, err
	}
	if len(event.Payload) > 0 {
		var envelope map[string]any
		if err := json.Unmarshal(event.Payload, &envelope); err == nil {
			if session, ok := envelope["session"].(map[string]any); ok {
				session["freshness"] = CatalogFreshness{Generation: uint64(priorGeneration), ObservedAt: state.ObservedAt, Stale: false}
				if encoded, err := json.Marshal(envelope); err == nil {
					event.Payload = encoded
				}
			}
		}
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

// sessionRowMatchesTx reports whether registering the session would leave its
// stored session and runtime rows unchanged, so an unchanged catalog
// observation needs no write.
func sessionRowMatchesTx(tx *sql.Tx, s surface.Session) (bool, error) {
	var kind, name, cwd, status, transcript, source, transport, model string
	var pid, hasLocal int
	var lastActiveMS int64
	err := tx.QueryRow(`SELECT surface,name,cwd,pid,status,transcript,has_local,source,transport,configured_model,last_active_ms FROM sessions WHERE id=?`, s.ID).Scan(&kind, &name, &cwd, &pid, &status, &transcript, &hasLocal, &source, &transport, &model, &lastActiveMS)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	wantLastActive := int64(0)
	if !s.LastActive.IsZero() {
		wantLastActive = s.LastActive.UnixMilli()
	}
	if kind != string(s.Surface) || name != s.Name || cwd != s.Cwd || pid != s.PID || status != string(s.Status) || transcript != s.Transcript || hasLocal != b2i(s.HasLocal) || source != s.Source || transport != s.Transport || (s.ConfiguredModel != "" && model != s.ConfiguredModel) || lastActiveMS != wantLastActive {
		return false, nil
	}
	if s.Runtime != nil {
		location, err := json.Marshal(s.Runtime.Location)
		if err != nil {
			return false, err
		}
		var launcher string
		var storedLocation []byte
		var focusable int
		err = tx.QueryRow(`SELECT runtime_launcher,runtime_location,runtime_focusable FROM session_runtime WHERE session_id=?`, s.ID).Scan(&launcher, &storedLocation, &focusable)
		if err == sql.ErrNoRows {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		if launcher != string(s.Runtime.Launcher) || string(storedLocation) != string(location) || focusable != b2i(s.Runtime.Focusable) {
			return false, nil
		}
	}
	if s.Surface == surface.KindClaude && s.Transcript != "" {
		var duplicate string
		err := tx.QueryRow(claudeDuplicateQuery+` LIMIT 1`, string(surface.KindClaude), s.ID, s.Transcript, claudeConversationID(s.Transcript)).Scan(&duplicate)
		if err == nil {
			return false, nil
		}
		if err != sql.ErrNoRows {
			return false, err
		}
	}
	return true, nil
}

// MarkCatalogDiscoveryFailure preserves the last discovered rows while making
// the failed observation visible to snapshot readers and catalog subscribers.
func (r *Registry) MarkCatalogDiscoveryFailure(kind surface.SurfaceKind, reason string, observedAt time.Time) ([]CatalogEvent, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return nil, err
	}
	if kind == "" || strings.TrimSpace(reason) == "" {
		return nil, fmt.Errorf("catalog discovery failure surface and reason are required")
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	} else {
		observedAt = observedAt.UTC()
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT cs.session_id,cs.projection_fingerprint,cs.projection_generation,cs.observed_at,s.surface,s.name,s.cwd,s.status FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.surface=?`, string(kind))
	if err != nil {
		return nil, err
	}
	type failedRow struct {
		id, fingerprint string
		generation      int64
		observedAt      string
		surface         string
		name, cwd       string
		status          string
	}
	var failed []failedRow
	for rows.Next() {
		var row failedRow
		if err := rows.Scan(&row.id, &row.fingerprint, &row.generation, &row.observedAt, &row.surface, &row.name, &row.cwd, &row.status); err != nil {
			rows.Close()
			return nil, err
		}
		failed = append(failed, row)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	events := make([]CatalogEvent, 0, len(failed))
	for _, row := range failed {
		generation := row.generation + 1
		if _, err := tx.Exec(`UPDATE catalog_sessions SET misses=0,discovery_failures=discovery_failures+1,projection_generation=? WHERE session_id=?`, generation, row.id); err != nil {
			return nil, err
		}
		lastObserved, err := time.Parse(time.RFC3339Nano, row.observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse catalog observation: %w", err)
		}
		var session map[string]any
		if err := json.Unmarshal([]byte(row.fingerprint), &session); err != nil {
			session = map[string]any{"id": row.id, "surface": row.surface, "name": row.name, "cwd": row.cwd, "status": row.status}
		}
		session["freshness"] = CatalogFreshness{Generation: uint64(generation), ObservedAt: lastObserved, Stale: true}
		envelope, err := json.Marshal(map[string]any{"session": session})
		if err != nil {
			return nil, err
		}
		event, created, err := r.AppendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("session.upserted:%s:%d", row.id, generation), Type: "session.upserted", EntityID: row.id, Payload: envelope})
		if err != nil {
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

func (r *Registry) UpdateCatalogSessionProjection(sessionID, priorFingerprint, nextFingerprint string) (CatalogEvent, bool, error) {
	if sessionID == "" || nextFingerprint == "" {
		return CatalogEvent{}, false, fmt.Errorf("catalog session id and projection fingerprint are required")
	}
	var session map[string]any
	if err := json.Unmarshal([]byte(nextFingerprint), &session); err != nil {
		return CatalogEvent{}, false, fmt.Errorf("catalog projection fingerprint must be a JSON object: %w", err)
	}
	tx, err := r.db.Begin()
	if err != nil {
		return CatalogEvent{}, false, err
	}
	defer tx.Rollback()
	if err := validateQueueProjectionTx(tx, sessionID, []byte(nextFingerprint), nil); err != nil {
		return CatalogEvent{}, false, err
	}
	var fingerprint, observedAt string
	var generation, misses, discoveryFailures int64
	err = tx.QueryRow(`SELECT projection_fingerprint,projection_generation,misses,discovery_failures,observed_at FROM catalog_sessions WHERE session_id=?`, sessionID).Scan(&fingerprint, &generation, &misses, &discoveryFailures, &observedAt)
	if err == sql.ErrNoRows || (err == nil && (fingerprint != priorFingerprint || fingerprint == nextFingerprint)) {
		return CatalogEvent{}, false, nil
	}
	if err != nil {
		return CatalogEvent{}, false, err
	}
	lastObserved, err := time.Parse(time.RFC3339Nano, observedAt)
	if err != nil {
		return CatalogEvent{}, false, fmt.Errorf("parse catalog observation: %w", err)
	}
	generation++
	if _, err := tx.Exec(`UPDATE catalog_sessions SET projection_fingerprint=?,projection_generation=? WHERE session_id=?`, nextFingerprint, generation, sessionID); err != nil {
		return CatalogEvent{}, false, err
	}
	session["observedAt"] = lastObserved
	session["freshness"] = CatalogFreshness{Generation: uint64(generation), ObservedAt: lastObserved, Stale: misses > 0 || discoveryFailures > 0}
	envelope, err := json.Marshal(map[string]any{"session": session})
	if err != nil {
		return CatalogEvent{}, false, err
	}
	event, created, err := r.AppendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("session.upserted:%s:%d", sessionID, generation), Type: "session.upserted", EntityID: sessionID, Payload: envelope})
	if err != nil {
		return CatalogEvent{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogEvent{}, false, err
	}
	return event, created, nil
}

// RecordCatalogDiscovery closes one successful discovery pass of a surface in
// a single transaction. Every session the pass saw keeps a fresh row, so its
// observed_at advances to the pass observation time without rewriting the
// row's projection. When the listing is complete, rows it omitted count a miss
// and are removed once they reach the threshold.
func (r *Registry) RecordCatalogDiscovery(kind surface.SurfaceKind, seen map[string]struct{}, observedAt time.Time, complete bool, threshold int) ([]CatalogEvent, error) {
	if err := r.EnsureCatalogState(); err != nil {
		return nil, err
	}
	if threshold < 1 {
		return nil, fmt.Errorf("catalog omission threshold must be positive")
	}
	if observedAt.IsZero() {
		return nil, fmt.Errorf("catalog discovery observation time is required")
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	encodedIDs, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE catalog_sessions SET observed_at=? WHERE session_id IN (SELECT value FROM json_each(?)) AND misses=0 AND discovery_failures=0`, observedAt.UTC().Format(time.RFC3339Nano), string(encodedIDs)); err != nil {
		return nil, err
	}
	if !complete {
		return nil, tx.Commit()
	}
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
	tx, err := r.read.Begin()
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
		cs.host_project,cs.checkout,cs.unavailable_reason,cs.observed_at,cs.projection_fingerprint,cs.projection_generation,cs.misses,cs.discovery_failures,COALESCE(sr.runtime_launcher,''),sr.runtime_location,COALESCE(sr.runtime_focusable,0),` + sessionSubagentSelect + `
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id LEFT JOIN session_runtime sr ON sr.session_id=s.id
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
		var generation, misses, discoveryFailures int64
		var launcher string
		var location []byte
		var focusable int
		var subagent subagentColumns
		if err := rows.Scan(append([]any{&state.Session.ID, &kind, &state.Session.Name, &state.Session.Cwd, &state.Session.PID, &status, &state.Session.Transcript, &hasLocal, &state.Session.Source, &state.Session.Transport, &state.Session.ConfiguredModel, &lastActiveMS, &state.HostProject, &state.Checkout, &state.UnavailableReason, &observedAt, &state.ProjectionFingerprint, &generation, &misses, &discoveryFailures, &launcher, &location, &focusable}, subagent.targets()...)...); err != nil {
			return CatalogSnapshot{}, err
		}
		subagent.apply(&state.Session)
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
		state.Freshness = CatalogFreshness{Generation: uint64(generation), ObservedAt: parsed, Stale: misses > 0 || discoveryFailures > 0}
		if runtime, err := runtimeFromColumns(launcher, location, focusable); err != nil {
			return CatalogSnapshot{}, err
		} else {
			state.Session.Runtime = runtime
		}
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

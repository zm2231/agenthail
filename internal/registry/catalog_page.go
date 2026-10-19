package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// CatalogPageRequest is the SQL-level projection of the dashboard catalog
// filters. Offset is used by the existing opaque dashboard cursor.
type CatalogPageRequest struct {
	Scope     string
	ProjectID string
	// ParentID lists every subagent of one session, including the ones a
	// session list hides.
	ParentID string
	Query    string
	Offset   int
	Limit    int
	Now      time.Time
}

type CatalogPage struct {
	CatalogSnapshot
	TotalMatching int
	HasMore       bool
	Offset        int
	Limit         int
}

func (r *Registry) CatalogSnapshotPage(request CatalogPageRequest) (CatalogPage, error) {
	if request.Limit < 1 || request.Limit > 200 {
		return CatalogPage{}, fmt.Errorf("catalog page limit must be between 1 and 200")
	}
	if request.Offset < 0 {
		return CatalogPage{}, fmt.Errorf("catalog page offset must not be negative")
	}
	switch request.Scope {
	case "", "all", "recent", "current", "running", "archived":
	default:
		return CatalogPage{}, fmt.Errorf("invalid catalog page scope %q", request.Scope)
	}
	if request.Now.IsZero() {
		request.Now = time.Now().UTC()
	}

	tx, err := r.read.Begin()
	if err != nil {
		return CatalogPage{}, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return CatalogPage{}, err
	}
	var latest sql.NullInt64
	if err := tx.QueryRow(`SELECT MAX(seq) FROM catalog_events`).Scan(&latest); err != nil {
		return CatalogPage{}, err
	}
	seq := uint64(0)
	if latest.Valid {
		if latest.Int64 < 0 {
			return CatalogPage{}, fmt.Errorf("invalid catalog sequence")
		}
		seq = uint64(latest.Int64)
	}
	listing, err := catalogListingTx(tx)
	if err != nil {
		return CatalogPage{}, err
	}
	from, where, args := catalogPageSQL(request, listing)
	if request.ParentID != "" {
		listing.Hidden, listing.Rollups = map[string]bool{}, listing.descendants
	}
	var total int
	if err := tx.QueryRow(`SELECT COUNT(*) `+from+` WHERE `+where, args...).Scan(&total); err != nil {
		return CatalogPage{}, err
	}
	rowArgs := append(append([]any{}, args...), request.Limit+1, request.Offset)
	rows, err := tx.Query(`SELECT s.id,s.host_id,s.surface,s.name,s.cwd,s.pid,s.status,s.transcript,s.has_local,s.source,s.transport,s.runtime_kind,s.configured_model,s.bridge,s.forked_from,s.last_active_ms,
		cs.host_project,cs.checkout,cs.unavailable_reason,cs.observed_at,cs.projection_fingerprint,cs.projection_generation,cs.misses,cs.discovery_failures,cs.waiting_on,
		COALESCE(sr.runtime_launcher,''),sr.runtime_location,COALESCE(sr.runtime_focusable,0),`+sessionSubagentSelect+`
		`+from+` WHERE `+where+` ORDER BY CASE WHEN s.status=? THEN 0 ELSE 1 END,s.last_active_ms DESC,s.updated_at DESC,s.id LIMIT ? OFFSET ?`, append(rowArgs[:len(rowArgs)-2], string(surface.StatusBusy), rowArgs[len(rowArgs)-2], rowArgs[len(rowArgs)-1])...)
	if err != nil {
		return CatalogPage{}, err
	}
	sessions := make([]CatalogSessionState, 0, request.Limit)
	for rows.Next() {
		state, err := scanCatalogSession(rows)
		if err != nil {
			rows.Close()
			return CatalogPage{}, err
		}
		sessions = append(sessions, state)
	}
	if err := rows.Close(); err != nil {
		return CatalogPage{}, err
	}
	if err := rows.Err(); err != nil {
		return CatalogPage{}, err
	}
	hasMore := len(sessions) > request.Limit
	if hasMore {
		sessions = sessions[:request.Limit]
	}
	surfaces, err := catalogSurfaceSnapshotTx(tx)
	if err != nil {
		return CatalogPage{}, err
	}
	if err := tx.Commit(); err != nil {
		return CatalogPage{}, err
	}
	return CatalogPage{CatalogSnapshot: CatalogSnapshot{HostEpoch: epoch, CatalogSeq: seq, Sessions: sessions, Surfaces: surfaces, Listing: listing}, TotalMatching: total, HasMore: hasMore, Offset: request.Offset, Limit: request.Limit}, nil
}

func catalogPageSQL(request CatalogPageRequest, listing CatalogListing) (string, string, []any) {
	from := `FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		LEFT JOIN aliases a ON a.session_id=s.id
		LEFT JOIN (SELECT session_id,COUNT(*) AS queue_count FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id) qc ON qc.session_id=s.id
		LEFT JOIN session_runtime sr ON sr.session_id=s.id`
	args := []any{}
	conditions := []string{}
	if request.ParentID != "" {
		conditions = append(conditions, "s.parent_session_id=?")
		args = append(args, request.ParentID)
	} else {
		conditions = append(conditions, "s.id NOT IN (SELECT value FROM json_each(?))")
		args = append(args, listing.hiddenJSON())
	}
	if request.ProjectID != "" {
		conditions = append(conditions, "json_extract(cs.host_project,'$.id')=?")
		args = append(args, request.ProjectID)
	}
	if query := strings.ToLower(strings.TrimSpace(request.Query)); query != "" {
		conditions = append(conditions, "instr(lower(s.name||' '||s.cwd||' '||coalesce(a.name,json_extract(cs.projection_fingerprint,'$.alias'),'')),?)>0")
		args = append(args, query)
	}
	archived := "COALESCE(json_extract(cs.projection_fingerprint,'$.archived'),0)=1"
	switch request.Scope {
	case "running":
		conditions = append(conditions, "NOT "+archived, "(s.status=? OR COALESCE(json_extract(cs.projection_fingerprint,'$.subagents.working'),0)>0)")
		args = append(args, string(surface.StatusBusy))
	case "current", "recent":
		conditions = append(conditions, "NOT "+archived, "(COALESCE(qc.queue_count,0)>0 OR (COALESCE(json_extract(cs.projection_fingerprint,'$.current'),0)=1 AND COALESCE(json_extract(cs.projection_fingerprint,'$.currentReason'),'')!='queued'))")
	case "all":
		conditions = append(conditions, "NOT "+archived)
	case "archived":
		conditions = append(conditions, archived)
	}
	return from, strings.Join(conditions, " AND "), args
}

func scanCatalogSession(scanner interface{ Scan(...any) error }) (CatalogSessionState, error) {
	var state CatalogSessionState
	var kind, status, observedAt string
	var hasLocal, focusable, generation, misses, discoveryFailures int
	var lastActiveMS int64
	var launcher string
	var location, waitingOn []byte
	var subagent subagentColumns
	if err := scanner.Scan(append([]any{&state.Session.ID, &state.HostID, &kind, &state.Session.Name, &state.Session.Cwd, &state.Session.PID, &status, &state.Session.Transcript, &hasLocal, &state.Session.Source, &state.Session.Transport, &state.Session.RuntimeKind, &state.Session.ConfiguredModel, &state.Session.Bridge, &state.Session.ForkedFrom, &lastActiveMS, &state.Project, &state.Checkout, &state.UnavailableReason, &observedAt, &state.ProjectionFingerprint, &generation, &misses, &discoveryFailures, &waitingOn, &launcher, &location, &focusable}, subagent.targets()...)...); err != nil {
		return CatalogSessionState{}, err
	}
	if len(waitingOn) > 0 {
		if err := json.Unmarshal(waitingOn, &state.Session.WaitingOn); err != nil {
			return CatalogSessionState{}, fmt.Errorf("parse catalog waiting state: %w", err)
		}
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
		return CatalogSessionState{}, fmt.Errorf("parse catalog observation: %w", err)
	}
	state.ObservedAt = parsed
	state.Freshness = CatalogFreshness{Generation: uint64(generation), ObservedAt: parsed, Stale: misses > 0 || discoveryFailures > 0 || (state.HostID != "" && state.UnavailableReason != "")}
	runtime, err := runtimeFromColumns(launcher, location, focusable)
	if err != nil {
		return CatalogSessionState{}, err
	}
	state.Session.Runtime = runtime
	return state, nil
}

func catalogSurfaceSnapshotTx(tx *sql.Tx) ([]CatalogSurfaceState, error) {
	rows, err := tx.Query(`SELECT surface,health,detail,observed_at FROM catalog_surfaces ORDER BY surface`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CatalogSurfaceState{}
	for rows.Next() {
		var state CatalogSurfaceState
		var observedAt string
		if err := rows.Scan(&state.Surface, &state.Health, &state.Detail, &observedAt); err != nil {
			return nil, err
		}
		parsed, err := time.Parse(time.RFC3339Nano, observedAt)
		if err != nil {
			return nil, fmt.Errorf("parse catalog surface observation: %w", err)
		}
		state.ObservedAt = parsed
		result = append(result, state)
	}
	return result, rows.Err()
}

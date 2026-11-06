package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type CatalogPresentationSubagent struct {
	ParentID, RootID string
	Depth            int
	Nickname, Role   string
}

type CatalogPresentationRow struct {
	ID, HostID                                   string
	Name, Surface, Status, Cwd                   string
	RuntimeKind, Source, Transport               string
	UnavailableReason                            string
	LastActive, RegisteredAt                     time.Time
	Subagent                                     *CatalogPresentationSubagent
	WaitingOn, BackgroundTasks, UsageLimit       json.RawMessage
	Alias                                        sql.NullString
	QueueCount                                   int
	Archived                                     bool
	Freshness                                    CatalogFreshness
	Attention                                    string
	Retired                                      bool
	PinnedAt                                     time.Time
	KeepAlive                                    KeepAlive
}

type CatalogPresentation struct {
	Outgoing     map[string]int
	RefreshAt    time.Time
	HostEpoch    string
	CatalogSeq   uint64
	Rows         []CatalogPresentationRow
	Limits       map[string]LimitResume
	Fingerprints map[string]json.RawMessage
	History      bool
	Retired      []CatalogRetiredCount
}

func (r *Registry) ReadCatalogPresentation(ctx context.Context, stale func(id string, generation uint64) bool) (CatalogPresentation, error) {
	return r.readCatalogPresentation(ctx, true, stale)
}

func (r *Registry) ReadCurrentCatalogPresentation(ctx context.Context, stale func(id string, generation uint64) bool) (CatalogPresentation, error) {
	return r.readCatalogPresentation(ctx, false, stale)
}

func (r *Registry) readCatalogPresentation(ctx context.Context, history bool, stale func(id string, generation uint64) bool) (CatalogPresentation, error) {
	tx, err := r.read.BeginTx(ctx, nil)
	if err != nil {
		return CatalogPresentation{}, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return CatalogPresentation{}, err
	}
	var seq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM catalog_events`).Scan(&seq); err != nil {
		return CatalogPresentation{}, err
	}
	read := CatalogPresentation{HostEpoch: epoch, CatalogSeq: uint64(seq.Int64), Rows: []CatalogPresentationRow{}, Fingerprints: map[string]json.RawMessage{}}
	if read.Rows, err = catalogPresentationRows(ctx, tx, history); err != nil {
		return CatalogPresentation{}, err
	}
	read.History = history
	if !history {
		if read.Retired, err = r.catalogRetiredCounts(ctx, tx); err != nil {
			return CatalogPresentation{}, err
		}
	}
	if read.Limits, err = catalogPresentationLimits(ctx, tx); err != nil {
		return CatalogPresentation{}, err
	}
	read.Outgoing, read.RefreshAt, err = catalogPresentationOutgoing(ctx, tx)
	if err != nil {
		return CatalogPresentation{}, err
	}
	var needed []string
	for _, row := range read.Rows {
		if stale(row.ID, row.Freshness.Generation) {
			needed = append(needed, row.ID)
		}
	}
	if read.Fingerprints, err = catalogFingerprints(ctx, tx, needed); err != nil {
		return CatalogPresentation{}, err
	}
	return read, tx.Commit()
}

func catalogPresentationRows(ctx context.Context, tx *sql.Tx, history bool) ([]CatalogPresentationRow, error) {
	from := `FROM catalog_sessions cs`
	if !history {
		from = `FROM (` + catalogUnretiredSessions + `) unretired JOIN catalog_sessions cs ON cs.session_id=unretired.session_id`
	}
	rows, err := tx.QueryContext(ctx, catalogPresentationSelect+from+catalogPresentationJoins)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []CatalogPresentationRow{}
	for rows.Next() {
		var row CatalogPresentationRow
		var lastActive int64
		var observed, registered string
		var subagent CatalogPresentationSubagent
		var waiting, background, limit string
		var pinnedAt, keepAliveFailedAt int64
		if err := rows.Scan(&row.ID, &row.HostID, &row.Name, &row.Surface, &row.Status, &row.Cwd, &lastActive, &row.RuntimeKind, &row.Source, &row.Transport,
			&row.UnavailableReason, &subagent.ParentID, &subagent.RootID, &subagent.Depth, &subagent.Nickname, &subagent.Role, &waiting, &background, &limit,
			&row.Alias, &row.QueueCount, &row.Archived, &observed, &row.Freshness.Generation, &row.Freshness.Stale, &row.Attention, &row.Retired, &registered,
			&pinnedAt, &keepAliveFailedAt, &row.KeepAlive.Error); err != nil {
			return nil, err
		}
		row.PinnedAt, row.KeepAlive.FailedAt = unixMilliOrZero(pinnedAt), unixMilliOrZero(keepAliveFailedAt)
		if row.RegisteredAt, err = parseRegisteredAt(registered); err != nil {
			return nil, err
		}
		if lastActive > 0 {
			row.LastActive = time.UnixMilli(lastActive).UTC()
		}
		if subagent.ParentID != "" {
			row.Subagent = &subagent
		}
		row.WaitingOn, row.BackgroundTasks, row.UsageLimit = presentationJSON(waiting), presentationJSON(background), presentationJSON(limit)
		if row.Freshness.ObservedAt, err = parseTimestamp(observed); err != nil {
			return nil, err
		}
		result = append(result, row)
	}
	return result, rows.Err()
}

func presentationJSON(raw string) json.RawMessage {
	if raw == "" || raw == "null" {
		return nil
	}
	return json.RawMessage(raw)
}

func catalogPresentationLimits(ctx context.Context, tx *sql.Tx) (map[string]LimitResume, error) {
	rows, err := tx.QueryContext(ctx, `SELECT session_id,state,refused_at_ms,resets_at_ms FROM limit_resumes WHERE state!='released'`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	limits := map[string]LimitResume{}
	for rows.Next() {
		var limit LimitResume
		var refused, resets int64
		if err := rows.Scan(&limit.SessionID, &limit.State, &refused, &resets); err != nil {
			return nil, err
		}
		if refused > 0 {
			limit.RefusedAt = time.UnixMilli(refused).UTC()
		}
		if resets > 0 {
			limit.ResetsAt = time.UnixMilli(resets).UTC()
		}
		limits[limit.SessionID] = limit
	}
	return limits, rows.Err()
}

func catalogFingerprints(ctx context.Context, tx *sql.Tx, ids []string) (map[string]json.RawMessage, error) {
	result := make(map[string]json.RawMessage, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT session_id,projection_fingerprint FROM catalog_sessions WHERE session_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, data string
		if err := rows.Scan(&id, &data); err != nil {
			return nil, err
		}
		if data == "" {
			data = "{}"
		}
		result[id] = json.RawMessage(data)
	}
	return result, rows.Err()
}

const catalogPresentationSelect = `SELECT s.id,s.host_id,s.name,s.surface,s.status,s.cwd,s.last_active_ms,s.runtime_kind,s.source,s.transport,
 CASE WHEN s.host_id!='' THEN cs.unavailable_reason ELSE '' END,s.parent_session_id,s.root_session_id,s.subagent_depth,s.agent_nickname,s.agent_role,
 CAST(cs.waiting_on AS TEXT),CAST(cs.background_tasks AS TEXT),CAST(cs.usage_limit AS TEXT),
 a.name,cs.host_queue_count+COALESCE(q.n,0),
 sa.session_id IS NOT NULL OR COALESCE(p.archived_at_ms,0)>0,
 cs.observed_at,cs.projection_generation,(cs.misses>0 OR cs.discovery_failures>0 OR (s.host_id!='' AND cs.unavailable_reason!='')),
 COALESCE(at.reason,''),cs.retired,s.registered_at,s.pinned_at_ms,COALESCE(ka.failed_at_ms,0),COALESCE(ka.error,'')
 `

const catalogPresentationJoins = ` JOIN sessions s ON s.id=cs.session_id
 LEFT JOIN aliases a ON a.session_id=s.id
 LEFT JOIN (SELECT session_id,COUNT(*) n FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id) q ON q.session_id=s.id
 LEFT JOIN session_archive sa ON sa.session_id=s.id
 LEFT JOIN session_keep_alive ka ON ka.session_id=s.id AND s.pinned_at_ms>0
 LEFT JOIN projects p ON p.id=json_extract(cs.host_project,'$.localId')
 LEFT JOIN (SELECT session_id,group_concat(reason,'; ') reason FROM attention_items WHERE resolved_at='' GROUP BY session_id) at ON at.session_id=s.id
 ORDER BY cs.session_id`

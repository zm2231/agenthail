package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"
)

type CatalogPresentationRow struct {
	ID, HostID string
	Data       json.RawMessage
	Freshness  CatalogFreshness
	Attention  string
}

type CatalogPresentation struct {
	Outgoing   map[string]int
	RefreshAt  time.Time
	HostEpoch  string
	CatalogSeq uint64
	Rows       []CatalogPresentationRow
	Limits     map[string]LimitResume
}

func (r *Registry) ReadCatalogPresentation(ctx context.Context, selectRows func(CatalogPresentation) ([]string, error)) (map[string]json.RawMessage, error) {
	tx, err := r.read.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return nil, err
	}
	var seq sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT MAX(seq) FROM catalog_events`).Scan(&seq); err != nil {
		return nil, err
	}
	read := CatalogPresentation{HostEpoch: epoch, CatalogSeq: uint64(seq.Int64), Rows: []CatalogPresentationRow{}}
	rows, err := tx.QueryContext(ctx, catalogPresentationSQL)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var row CatalogPresentationRow
		var raw, observed string
		if err := rows.Scan(&row.ID, &row.HostID, &raw, &observed, &row.Freshness.Generation, &row.Freshness.Stale, &row.Attention); err != nil {
			rows.Close()
			return nil, err
		}
		row.Data = json.RawMessage(raw)
		row.Freshness.ObservedAt, err = parseTimestamp(observed)
		if err != nil {
			rows.Close()
			return nil, err
		}
		read.Rows = append(read.Rows, row)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	read.Limits = map[string]LimitResume{}
	limits, err := tx.QueryContext(ctx, `SELECT session_id,state,refused_at_ms,resets_at_ms FROM limit_resumes WHERE state!='released'`)
	if err != nil {
		return nil, err
	}
	for limits.Next() {
		var limit LimitResume
		var refused, resets int64
		if err := limits.Scan(&limit.SessionID, &limit.State, &refused, &resets); err != nil {
			limits.Close()
			return nil, err
		}
		if refused > 0 {
			limit.RefusedAt = time.UnixMilli(refused).UTC()
		}
		if resets > 0 {
			limit.ResetsAt = time.UnixMilli(resets).UTC()
		}
		read.Limits[limit.SessionID] = limit
	}
	err = limits.Err()
	limits.Close()
	if err != nil {
		return nil, err
	}
	read.Outgoing, read.RefreshAt, err = catalogPresentationOutgoing(ctx, tx)
	if err != nil {
		return nil, err
	}
	ids, err := selectRows(read)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	full, err := tx.QueryContext(ctx, `SELECT session_id,projection_fingerprint FROM catalog_sessions WHERE session_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return nil, err
	}
	result := make(map[string]json.RawMessage, len(ids))
	for full.Next() {
		var id, data string
		if err := full.Scan(&id, &data); err != nil {
			full.Close()
			return nil, err
		}
		if data == "" {
			data = "{}"
		}
		result[id] = json.RawMessage(data)
	}
	err = full.Err()
	full.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

const catalogPresentationSQL = `SELECT s.id,s.host_id,
 json_patch(json_remove(COALESCE(NULLIF(cs.projection_fingerprint,''),'{}'),
 '$.holders','$.plan','$.runtime','$.claudeConfigRoot','$.configuredModel','$.pendingModel'),
 json_object('name',s.name,'surface',s.surface,'status',s.status,'cwd',s.cwd,
 'lastActive',CASE WHEN s.last_active_ms>0 THEN strftime('%Y-%m-%dT%H:%M:%fZ',s.last_active_ms/1000.0,'unixepoch') ELSE NULL END,
 'runtimeKind',s.runtime_kind,'source',s.source,'transport',s.transport,
 'unavailableReason',cs.unavailable_reason,
 'subagent',CASE WHEN s.parent_session_id!='' THEN json_object('parentId',s.parent_session_id,'rootId',s.root_session_id,'depth',s.subagent_depth,'nickname',s.agent_nickname,'role',s.agent_role) ELSE NULL END,
 'waitingOn',json(COALESCE(NULLIF(CAST(cs.waiting_on AS TEXT),''),'null')),
 'backgroundTasks',json(COALESCE(NULLIF(CAST(cs.background_tasks AS TEXT),''),'null')),
 'usageLimit',json(COALESCE(NULLIF(CAST(cs.usage_limit AS TEXT),''),'null')),
 'alias',CASE WHEN s.host_id='' THEN COALESCE(a.name,'') ELSE COALESCE(a.name,json_extract(cs.projection_fingerprint,'$.alias'),'') END,
 'queueCount',cs.host_queue_count+COALESCE(q.n,0),
 'archived',json(CASE WHEN s.host_id!='' THEN CASE WHEN COALESCE(json_extract(cs.projection_fingerprint,'$.archived'),0)=1 THEN 'true' ELSE 'false' END
 ELSE CASE WHEN sa.session_id IS NOT NULL OR COALESCE(p.archived_at_ms,0)>0 THEN 'true' ELSE 'false' END END))),
 cs.observed_at,cs.projection_generation,(cs.misses>0 OR cs.discovery_failures>0 OR (s.host_id!='' AND cs.unavailable_reason!='')),
 COALESCE(at.reason,'')
 FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
 LEFT JOIN aliases a ON a.session_id=s.id
 LEFT JOIN (SELECT session_id,COUNT(*) n FROM message_queue WHERE status IN ('pending','inflight') GROUP BY session_id) q ON q.session_id=s.id
 LEFT JOIN session_archive sa ON sa.session_id=s.id
 LEFT JOIN projects p ON p.id=json_extract(cs.host_project,'$.localId')
 LEFT JOIN (SELECT session_id,group_concat(reason,'; ') reason FROM attention_items WHERE resolved_at='' GROUP BY session_id) at ON at.session_id=s.id
 ORDER BY s.id`

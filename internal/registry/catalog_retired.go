package registry

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/surface"
)

// A retired row has no parent and adds to no rollup, so current reads can skip it.
const catalogRetiredSchema = `
CREATE INDEX IF NOT EXISTS catalog_sessions_unretired ON catalog_sessions(session_id) WHERE retired=0;
CREATE INDEX IF NOT EXISTS catalog_sessions_retired ON catalog_sessions(projection_generation) WHERE retired=1;
DROP TRIGGER IF EXISTS catalog_sessions_retired_current;
CREATE TRIGGER catalog_sessions_retired_current AFTER UPDATE OF current ON catalog_sessions
WHEN NEW.current=1 AND NEW.retired=1
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE session_id=NEW.session_id;
END;
CREATE INDEX IF NOT EXISTS sessions_parent ON sessions(parent_session_id) WHERE parent_session_id<>'';
CREATE INDEX IF NOT EXISTS sessions_root ON sessions(root_session_id) WHERE root_session_id<>'';
DROP TRIGGER IF EXISTS sessions_unretire;
CREATE TRIGGER sessions_unretire AFTER UPDATE OF ended,parent_session_id ON sessions
WHEN (OLD.ended=1 AND NEW.ended=0) OR NEW.parent_session_id IS NOT OLD.parent_session_id
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE session_id=NEW.id AND retired=1;
END;
DROP TRIGGER IF EXISTS catalog_sessions_unretire_family;
CREATE TRIGGER catalog_sessions_unretire_family AFTER UPDATE OF retired ON catalog_sessions
WHEN OLD.retired=1 AND NEW.retired=0
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE retired=1 AND session_id IN (SELECT id FROM sessions WHERE parent_session_id<>'' AND parent_session_id=NEW.session_id
		UNION SELECT id FROM sessions WHERE root_session_id<>'' AND root_session_id IN (NEW.session_id,(SELECT NULLIF(root_session_id,'') FROM sessions WHERE id=NEW.session_id)));
END;
DROP TRIGGER IF EXISTS catalog_sessions_unretire_family_insert;
CREATE TRIGGER catalog_sessions_unretire_family_insert AFTER INSERT ON catalog_sessions
WHEN NEW.retired=0
BEGIN
	UPDATE catalog_sessions SET retired=0 WHERE retired=1 AND session_id IN (SELECT id FROM sessions WHERE parent_session_id<>'' AND parent_session_id=NEW.session_id
		UNION SELECT id FROM sessions WHERE root_session_id<>'' AND root_session_id IN (NEW.session_id,(SELECT NULLIF(root_session_id,'') FROM sessions WHERE id=NEW.session_id)));
END;
INSERT OR IGNORE INTO catalog_retired_version(id,version) VALUES(1,0);
DROP TRIGGER IF EXISTS catalog_retired_version_row;
CREATE TRIGGER catalog_retired_version_row AFTER UPDATE OF retired,host_project,unavailable_reason ON catalog_sessions
WHEN OLD.retired=1 OR NEW.retired=1
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_removed;
CREATE TRIGGER catalog_retired_version_removed AFTER DELETE ON catalog_sessions
WHEN OLD.retired=1
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_archived;
CREATE TRIGGER catalog_retired_version_archived AFTER INSERT ON session_archive
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_unarchived;
CREATE TRIGGER catalog_retired_version_unarchived AFTER DELETE ON session_archive
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_project_added;
CREATE TRIGGER catalog_retired_version_project_added AFTER INSERT ON projects
WHEN NEW.archived_at_ms>0
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_project;
CREATE TRIGGER catalog_retired_version_project AFTER UPDATE OF archived_at_ms ON projects
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;
DROP TRIGGER IF EXISTS catalog_retired_version_project_removed;
CREATE TRIGGER catalog_retired_version_project_removed AFTER DELETE ON projects
BEGIN
	UPDATE catalog_retired_version SET version=version+1;
END;`

const catalogRetiredRecency = 24 * time.Hour

const catalogNeededSessions = `SELECT session_id FROM message_queue WHERE status IN ('pending','inflight')
	UNION SELECT a.session_id FROM coordinators c JOIN aliases a ON a.name=c.name WHERE c.host_id=''
	UNION SELECT session_id FROM attention_items WHERE resolved_at=''
	UNION SELECT session_id FROM limit_resumes WHERE state!='released'
	UNION SELECT sender_session_id FROM delivery_intents WHERE status IN ('failed','held') AND COALESCE(dismissed_at,'')=''`

const catalogUnretiredSessions = `SELECT session_id FROM catalog_sessions INDEXED BY catalog_sessions_unretired WHERE retired=0
	UNION ALL SELECT session_id FROM catalog_sessions WHERE +retired=1 AND session_id IN (` + catalogNeededSessions + `)`

func sessionEnded(session surface.Session) bool {
	return session.Ended || session.Status == surface.StatusOffline
}

// A pass retires its own surface's rows that it still lists or keeps, never
// one partway to removal, and any mirrored row: its host decided to keep it.
// A subagent retires only under a retired parent or one with no row, so no
// unretired row loses part of its rollup, and a row that comes back or
// appears brings its whole family back.
// Each statement retires one more level of a family.
func retireCatalogRowsTx(tx *sql.Tx, kind surface.SurfaceKind, threshold int, now time.Time) error {
	for {
		result, err := tx.Exec(`UPDATE catalog_sessions INDEXED BY catalog_sessions_unretired SET retired=1 WHERE retired=0 AND current=0
			AND session_id IN (SELECT s.id FROM sessions s WHERE s.ended=1 AND s.last_active_ms<?
				AND (s.host_id<>'' OR (s.surface=? AND (catalog_sessions.misses=0 OR catalog_sessions.misses>=?)))
				AND CASE WHEN s.parent_session_id='' THEN s.source NOT IN ('subAgent','exec')
					ELSE NOT EXISTS (SELECT 1 FROM catalog_sessions p WHERE p.session_id=s.parent_session_id AND p.retired=0) END)
			AND session_id NOT IN (`+catalogNeededSessions+`)`, now.Add(-catalogRetiredRecency).UnixMilli(), string(kind), threshold)
		if err != nil {
			return err
		}
		if retired, err := result.RowsAffected(); err != nil || retired == 0 {
			return err
		}
	}
}

type CatalogEndedRow struct {
	ID, Transcript string
}

func (r *Registry) endedRows(query string, args ...any) ([]CatalogEndedRow, error) {
	return queryRows(r.read, []CatalogEndedRow{}, func(row rowScanner) (CatalogEndedRow, error) {
		var ended CatalogEndedRow
		err := row.Scan(&ended.ID, &ended.Transcript)
		return ended, err
	}, query, args...)
}

func (r *Registry) CatalogOmissionCandidates(kind string, threshold int, recheck bool) ([]CatalogEndedRow, error) {
	if recheck {
		return r.endedRows(`SELECT cs.session_id,s.transcript FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.surface=? AND s.host_id='' AND cs.misses>=?`, kind, threshold-1)
	}
	return r.endedRows(`SELECT cs.session_id,s.transcript FROM catalog_sessions cs INDEXED BY catalog_sessions_unretired JOIN sessions s ON s.id=cs.session_id WHERE cs.retired=0 AND s.surface=? AND s.host_id='' AND cs.misses=?`, kind, threshold-1)
}

func (r *Registry) EndedSessionsWithoutCatalogRow(kind string) ([]CatalogEndedRow, error) {
	return r.endedRows(`SELECT s.id,s.transcript FROM sessions s WHERE s.surface=? AND s.host_id='' AND s.transcript<>'' AND s.parent_session_id=''
		AND NOT EXISTS (SELECT 1 FROM catalog_sessions cs WHERE cs.session_id=s.id) ORDER BY s.last_active_ms DESC,s.id`, kind)
}

func (r *Registry) LeadSessionsWithoutCatalogRow(kind string) ([]CatalogEndedRow, error) {
	return r.endedRows(`SELECT s.id,s.transcript FROM sessions s WHERE s.surface=? AND s.host_id='' AND s.id IN (`+localLeadSessions+`)
		AND NOT EXISTS (SELECT 1 FROM catalog_sessions cs WHERE cs.session_id=s.id)`, kind)
}

// The current listing alone can't tell a retired row from one it has never seen.
func (r *Registry) CatalogListingForEntities(entities []string) (CatalogListing, error) {
	tx, err := r.read.Begin()
	if err != nil {
		return CatalogListing{}, err
	}
	defer tx.Rollback()
	listing, err := catalogCurrentListingTx(tx)
	if err != nil {
		return CatalogListing{}, err
	}
	ids := make([]string, 0, len(entities))
	for _, entity := range entities {
		if _, id, ok := hosts.Split(entity); ok {
			ids = append(ids, id)
		} else {
			ids = append(ids, entity)
		}
	}
	encoded, err := json.Marshal(ids)
	if err != nil {
		return CatalogListing{}, err
	}
	rows, err := tx.Query(`SELECT s.id,s.host_id FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		WHERE +cs.retired=1 AND cs.session_id IN (SELECT value FROM json_each(?))`, string(encoded))
	if err != nil {
		return CatalogListing{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, hostID string
		if err := rows.Scan(&id, &hostID); err != nil {
			return CatalogListing{}, err
		}
		listing.Retired[id] = true
		listing.retiredEntities[hosts.Qualify(hostID, id)] = true
	}
	return listing, rows.Err()
}

type CatalogRetiredCount struct {
	Surface, ProjectID string
	Status             surface.SessionStatus
	Archived           bool
	Unavailable        bool
	Sessions           int
}

type catalogRetiredCache struct {
	sync.Mutex
	valid   bool
	version int64
	counts  []CatalogRetiredCount
}

// Cached: grouping parses every retired row's project on the current-view path.
func (r *Registry) catalogRetiredCounts(ctx context.Context, tx *sql.Tx) ([]CatalogRetiredCount, error) {
	var version int64
	if err := tx.QueryRowContext(ctx, `SELECT version FROM catalog_retired_version WHERE id=1`).Scan(&version); err != nil {
		return nil, err
	}
	cache := &r.retiredCounts
	cache.Lock()
	defer cache.Unlock()
	if cache.valid && cache.version == version {
		return cache.counts, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT s.surface,s.status,COALESCE(json_extract(cs.host_project,'$.id'),''),
		sa.session_id IS NOT NULL OR COALESCE(p.archived_at_ms,0)>0,s.host_id<>'' AND cs.unavailable_reason<>'',COUNT(*)
		FROM catalog_sessions cs INDEXED BY catalog_sessions_retired JOIN sessions s ON s.id=cs.session_id
		LEFT JOIN session_archive sa ON sa.session_id=s.id
		LEFT JOIN projects p ON p.id=json_extract(cs.host_project,'$.localId')
		WHERE cs.retired=1 GROUP BY 1,2,3,4,5`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	counts := []CatalogRetiredCount{}
	for rows.Next() {
		var count CatalogRetiredCount
		if err := rows.Scan(&count.Surface, &count.Status, &count.ProjectID, &count.Archived, &count.Unavailable, &count.Sessions); err != nil {
			return nil, err
		}
		counts = append(counts, count)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	cache.valid, cache.version, cache.counts = true, version, counts
	return counts, nil
}

// IDs let a peer tell a row it kept from one removed while it was away.
type CatalogMirrorRetired struct {
	Clock int64
	IDs   []string
}

func catalogMirrorRetiredTx(q catalogQuerier) (CatalogMirrorRetired, error) {
	var read CatalogMirrorRetired
	read.IDs = []string{}
	if err := q.QueryRow(`SELECT COALESCE(MAX(projection_clock),0) FROM catalog_state`).Scan(&read.Clock); err != nil {
		return read, err
	}
	rows, err := q.Query(`SELECT cs.session_id FROM catalog_sessions cs INDEXED BY catalog_sessions_retired JOIN sessions s ON s.id=cs.session_id WHERE cs.retired=1 AND s.host_id=''`)
	if err != nil {
		return read, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return read, err
		}
		read.IDs = append(read.IDs, id)
	}
	return read, rows.Err()
}

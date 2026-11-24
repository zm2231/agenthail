package registry

import (
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

// CatalogRowMetadata is what a catalog row shows about its sessions beyond
// the session itself, read at once for a set of sessions. Every table it
// reads is a presentation input and Version is read before them, so while the
// presentation version stays at Version, a read now would return the same
// rows; a write that lands during the read moves the version past it.
type CatalogRowMetadata struct {
	Version  uint64
	sessions map[string]struct{}
	leads    map[string]bool
	starters map[string]string
	tasks    map[string]bool
	joined   map[string]time.Time
	work     map[string]WorkRecord
	prs      map[string][]SessionPR
	moves    map[string]MoveShown
}

// CatalogRow is one session's part of CatalogRowMetadata. RegisteredAt is
// zero for a session the registry does not hold.
type CatalogRow struct {
	Lead         bool
	StartedBy    string
	Task         bool
	RegisteredAt time.Time
	Work         WorkRecord
	HasWork      bool
	PRs          []SessionPR
	Move         *MoveShown
}

const sessionIDsIn = ` IN (SELECT value FROM json_each(?))`

func (r *Registry) CatalogRowMetadata(sessionIDs []string) (CatalogRowMetadata, error) {
	ids, err := json.Marshal(sessionIDs)
	if err != nil {
		return CatalogRowMetadata{}, err
	}
	meta := CatalogRowMetadata{sessions: make(map[string]struct{}, len(sessionIDs)), leads: map[string]bool{}, starters: map[string]string{}, tasks: map[string]bool{}, joined: map[string]time.Time{}, work: map[string]WorkRecord{}, prs: map[string][]SessionPR{}}
	for _, id := range sessionIDs {
		meta.sessions[id] = struct{}{}
	}
	if err := r.read.QueryRow(`SELECT version FROM catalog_presentation_version WHERE id=1`).Scan(&meta.Version); err != nil {
		return CatalogRowMetadata{}, err
	}
	if _, err := queryRows(r.read, nil, func(row rowScanner) (struct{}, error) {
		var id string
		err := row.Scan(&id)
		meta.leads[id] = true
		return struct{}{}, err
	}, localLeadSessions+` AND a.session_id`+sessionIDsIn, string(ids)); err != nil {
		return CatalogRowMetadata{}, err
	}
	if _, err := queryRows(r.read, nil, func(row rowScanner) (struct{}, error) {
		var id, starter, host string
		err := row.Scan(&id, &starter, &host)
		meta.starters[id] = hosts.Qualify(host, starter)
		return struct{}{}, err
	}, agentStarterSelect+` AND o.session_id`+sessionIDsIn, string(ids)); err != nil {
		return CatalogRowMetadata{}, err
	}
	if _, err := queryRows(r.read, nil, func(row rowScanner) (struct{}, error) {
		var id string
		err := row.Scan(&id)
		meta.tasks[id] = true
		return struct{}{}, err
	}, `SELECT DISTINCT child_session_id FROM tasks WHERE child_session_id`+sessionIDsIn, string(ids)); err != nil {
		return CatalogRowMetadata{}, err
	}
	if _, err := queryRows(r.read, nil, func(row rowScanner) (struct{}, error) {
		var id, registered string
		if err := row.Scan(&id, &registered); err != nil {
			return struct{}{}, err
		}
		at, err := parseRegisteredAt(registered)
		meta.joined[id] = at
		return struct{}{}, err
	}, `SELECT id,registered_at FROM sessions WHERE id`+sessionIDsIn, string(ids)); err != nil {
		return CatalogRowMetadata{}, err
	}
	work, err := queryWorkRecords(r.read, `WHERE session_id`+sessionIDsIn, string(ids))
	if err != nil {
		return CatalogRowMetadata{}, err
	}
	for _, record := range work {
		if _, found := meta.work[record.SessionID]; !found {
			meta.work[record.SessionID] = record
		}
	}
	prs, err := querySessionPRs(r.read, `WHERE session_id`+sessionIDsIn+` AND unlinked=0`, string(ids))
	if err != nil {
		return CatalogRowMetadata{}, err
	}
	for _, pr := range prs {
		meta.prs[pr.SessionID] = append(meta.prs[pr.SessionID], pr)
	}
	if meta.moves, err = unfinishedMovesOut(r.read, ` AND m.conversation`+sessionIDsIn, string(ids)); err != nil {
		return CatalogRowMetadata{}, err
	}
	return meta, nil
}

// Row returns a session's metadata; found is false for a session the read
// did not cover.
func (m CatalogRowMetadata) Row(sessionID string) (row CatalogRow, found bool) {
	if _, found = m.sessions[sessionID]; !found {
		return CatalogRow{}, false
	}
	row = CatalogRow{Lead: m.leads[sessionID], StartedBy: m.starters[sessionID], Task: m.tasks[sessionID], RegisteredAt: m.joined[sessionID], PRs: m.prs[sessionID]}
	row.Work, row.HasWork = m.work[sessionID]
	if move, moving := m.moves[sessionID]; moving {
		row.Move = &move
	}
	return row, true
}

// CatalogPresentationVersion moves whenever a value a session row shows
// changes.
func (r *Registry) CatalogPresentationVersion() (uint64, error) {
	var version uint64
	err := r.read.QueryRow(`SELECT version FROM catalog_presentation_version WHERE id=1`).Scan(&version)
	return version, err
}

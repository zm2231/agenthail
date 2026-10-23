package registry

import (
	"database/sql"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// CatalogPresenceRow is a local catalog row with the session fields presence
// and archive activity depend on. ProjectionFingerprint is set only when
// FingerprintRead is true.
type CatalogPresenceRow struct {
	Session               surface.Session
	Generation            int64
	ProjectionFingerprint string
	FingerprintRead       bool
}

type CatalogPresenceRead struct {
	Rows      []CatalogPresenceRow
	Listing   CatalogListing
	HostEpoch string
	Clock     int64
}

// CatalogPresenceRows reads, in one transaction with the listing, the local
// catalog rows and only the session fields presence and archive activity
// depend on, into rows' storage. A row's projection text is read when the row
// was written after clock in hostEpoch, or when known does not hold the row at
// its generation.
func (r *Registry) CatalogPresenceRows(rows []CatalogPresenceRow, hostEpoch string, clock int64, known func(id string, generation int64) bool) (CatalogPresenceRead, error) {
	tx, err := r.read.Begin()
	if err != nil {
		return CatalogPresenceRead{}, err
	}
	defer tx.Rollback()
	read := CatalogPresenceRead{Rows: rows[:0]}
	err = tx.QueryRow(`SELECT host_epoch,projection_clock FROM catalog_state WHERE id=1`).Scan(&read.HostEpoch, &read.Clock)
	if err != nil && err != sql.ErrNoRows {
		return CatalogPresenceRead{}, err
	}
	after := clock
	if err == sql.ErrNoRows || read.HostEpoch != hostEpoch {
		after = -1
	}
	result, err := tx.Query(`SELECT s.id,s.name,s.surface,s.status,s.runtime_kind,s.last_active_ms,cs.projection_generation,
		CASE WHEN cs.projection_generation>? THEN cs.projection_fingerprint END
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id=''`, after)
	if err != nil {
		return CatalogPresenceRead{}, err
	}
	var unknown []int
	var id, name, kind, status, runtimeKind string
	var lastActiveMS, generation int64
	var fingerprint sql.NullString
	for result.Next() {
		if err := result.Scan(&id, &name, &kind, &status, &runtimeKind, &lastActiveMS, &generation, &fingerprint); err != nil {
			result.Close()
			return CatalogPresenceRead{}, err
		}
		row := CatalogPresenceRow{Generation: generation, ProjectionFingerprint: fingerprint.String, FingerprintRead: fingerprint.Valid}
		row.Session.ID, row.Session.Name, row.Session.RuntimeKind = id, name, surface.RuntimeKind(runtimeKind)
		row.Session.Surface, row.Session.Status = surface.SurfaceKind(kind), surface.SessionStatus(status)
		if lastActiveMS > 0 {
			row.Session.LastActive = time.UnixMilli(lastActiveMS)
		}
		if !row.FingerprintRead && !known(id, generation) {
			unknown = append(unknown, len(read.Rows))
		}
		read.Rows = append(read.Rows, row)
	}
	if err := result.Err(); err != nil {
		result.Close()
		return CatalogPresenceRead{}, err
	}
	if err := result.Close(); err != nil {
		return CatalogPresenceRead{}, err
	}
	for _, index := range unknown {
		row := &read.Rows[index]
		if err := tx.QueryRow(`SELECT projection_fingerprint FROM catalog_sessions WHERE session_id=?`, row.Session.ID).Scan(&row.ProjectionFingerprint); err != nil {
			return CatalogPresenceRead{}, err
		}
		row.FingerprintRead = true
	}
	if read.Listing, err = catalogListingTx(tx); err != nil {
		return CatalogPresenceRead{}, err
	}
	return read, tx.Commit()
}

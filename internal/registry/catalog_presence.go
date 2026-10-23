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
// depend on. A row's projection text is read when the row was written after
// clock in hostEpoch, or when known does not hold the row at its generation.
func (r *Registry) CatalogPresenceRows(hostEpoch string, clock int64, known func(id string, generation int64) bool) (CatalogPresenceRead, error) {
	tx, err := r.read.Begin()
	if err != nil {
		return CatalogPresenceRead{}, err
	}
	defer tx.Rollback()
	read := CatalogPresenceRead{Rows: []CatalogPresenceRow{}}
	err = tx.QueryRow(`SELECT host_epoch,projection_clock FROM catalog_state WHERE id=1`).Scan(&read.HostEpoch, &read.Clock)
	if err != nil && err != sql.ErrNoRows {
		return CatalogPresenceRead{}, err
	}
	after := clock
	if err == sql.ErrNoRows || read.HostEpoch != hostEpoch {
		after = -1
	}
	rows, err := tx.Query(`SELECT s.id,s.name,s.surface,s.status,s.runtime_kind,s.last_active_ms,cs.projection_generation,
		CASE WHEN cs.projection_generation>? THEN cs.projection_fingerprint END
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id=''`, after)
	if err != nil {
		return CatalogPresenceRead{}, err
	}
	var unknown []int
	for rows.Next() {
		var row CatalogPresenceRow
		var kind, status string
		var lastActiveMS int64
		var fingerprint sql.NullString
		if err := rows.Scan(&row.Session.ID, &row.Session.Name, &kind, &status, &row.Session.RuntimeKind, &lastActiveMS, &row.Generation, &fingerprint); err != nil {
			rows.Close()
			return CatalogPresenceRead{}, err
		}
		row.Session.Surface = surface.SurfaceKind(kind)
		row.Session.Status = surface.SessionStatus(status)
		if lastActiveMS > 0 {
			row.Session.LastActive = time.UnixMilli(lastActiveMS)
		}
		row.ProjectionFingerprint, row.FingerprintRead = fingerprint.String, fingerprint.Valid
		if !row.FingerprintRead && !known(row.Session.ID, row.Generation) {
			unknown = append(unknown, len(read.Rows))
		}
		read.Rows = append(read.Rows, row)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return CatalogPresenceRead{}, err
	}
	if err := rows.Close(); err != nil {
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

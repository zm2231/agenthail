package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type CatalogPresenceRow struct {
	Session               surface.Session
	ProjectionFingerprint string
}

// CatalogPresenceRows reads, in one transaction with the listing, the local
// catalog rows and only the session fields presence and archive activity
// depend on.
func (r *Registry) CatalogPresenceRows() ([]CatalogPresenceRow, CatalogListing, error) {
	tx, err := r.read.Begin()
	if err != nil {
		return nil, CatalogListing{}, err
	}
	defer tx.Rollback()
	rows, err := tx.Query(`SELECT s.id,s.name,s.surface,s.status,s.runtime_kind,s.last_active_ms,cs.projection_fingerprint
		FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE s.host_id=''`)
	if err != nil {
		return nil, CatalogListing{}, err
	}
	presence := []CatalogPresenceRow{}
	for rows.Next() {
		var row CatalogPresenceRow
		var kind, status string
		var lastActiveMS int64
		if err := rows.Scan(&row.Session.ID, &row.Session.Name, &kind, &status, &row.Session.RuntimeKind, &lastActiveMS, &row.ProjectionFingerprint); err != nil {
			rows.Close()
			return nil, CatalogListing{}, err
		}
		row.Session.Surface = surface.SurfaceKind(kind)
		row.Session.Status = surface.SessionStatus(status)
		if lastActiveMS > 0 {
			row.Session.LastActive = time.UnixMilli(lastActiveMS)
		}
		presence = append(presence, row)
	}
	if err := rows.Close(); err != nil {
		return nil, CatalogListing{}, err
	}
	listing, err := catalogListingTx(tx)
	if err != nil {
		return nil, CatalogListing{}, err
	}
	return presence, listing, tx.Commit()
}

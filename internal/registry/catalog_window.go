package registry

import "database/sql"

func (r *Registry) CatalogEventsAfter(after uint64, limit int) (CatalogEventWindow, error) {
	if limit < 1 {
		return CatalogEventWindow{Events: []CatalogEvent{}}, nil
	}
	tx, err := r.read.Begin()
	if err != nil {
		return CatalogEventWindow{}, err
	}
	defer tx.Rollback()
	epoch, err := catalogHostEpochTx(tx, false)
	if err != nil {
		return CatalogEventWindow{}, err
	}
	var earliest, latest sql.NullInt64
	if err := tx.QueryRow(`SELECT (SELECT MIN(seq) FROM catalog_events),(SELECT MAX(seq) FROM catalog_events)`).Scan(&earliest, &latest); err != nil {
		return CatalogEventWindow{}, err
	}
	window := CatalogEventWindow{Events: []CatalogEvent{}, HostEpoch: epoch}
	if !earliest.Valid || !latest.Valid {
		if err := tx.Commit(); err != nil {
			return CatalogEventWindow{}, err
		}
		return window, nil
	}
	window.EarliestSeq = uint64(earliest.Int64)
	window.LatestSeq = uint64(latest.Int64)
	window.Gap = after < window.EarliestSeq-1 || after > window.LatestSeq
	if window.Gap {
		if err := tx.Commit(); err != nil {
			return CatalogEventWindow{}, err
		}
		return window, nil
	}
	events, err := queryRows(tx, window.Events, scanCatalogEvent, `SELECT seq,dedupe_key,type,entity_id,payload,created_at FROM catalog_events WHERE seq>? ORDER BY seq LIMIT ?`, after, limit)
	if err != nil {
		return CatalogEventWindow{}, err
	}
	window.Events = events
	if err := tx.Commit(); err != nil {
		return CatalogEventWindow{}, err
	}
	return window, nil
}

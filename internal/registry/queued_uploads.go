package registry

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) QueuedUploadIDs(deadSince time.Time) ([]string, error) {
	rows, err := r.read.Query(`SELECT uploads FROM message_queue WHERE uploads NOT IN ('','[]') AND (status IN ('pending','inflight') OR status='dead' AND dead_at_ms>?)`, deadSince.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var stored surface.Uploads
		if err := rows.Scan(&stored); err != nil {
			return nil, err
		}
		for _, upload := range stored {
			ids = append(ids, upload.ID)
		}
	}
	return ids, rows.Err()
}

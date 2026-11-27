package daemon

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// Decisions and Work pages must read none of these fields of a busy session
// that neither waits on nor asks the person; catalog_progress_test.go holds
// them to it.
var catalogProgressFields = map[string]bool{"activity": true, "lastActive": true}

// catalogProgressTick reports an upsert that only advanced a working
// session's progress.
func catalogProgressTick(event registry.CatalogEvent) bool {
	changed, known := registry.SessionUpsertChanges(event)
	if !known || len(changed) == 0 {
		return false
	}
	for _, key := range changed {
		if !catalogProgressFields[key] {
			return false
		}
	}
	var envelope struct {
		Session struct {
			Status    surface.SessionStatus `json:"status"`
			WaitingOn json.RawMessage       `json:"waitingOn"`
			Ask       json.RawMessage       `json:"ask"`
		} `json:"session"`
	}
	if json.Unmarshal(event.Payload, &envelope) != nil {
		return false
	}
	row := envelope.Session
	return row.Status == surface.StatusBusy && absentJSON(row.WaitingOn) && absentJSON(row.Ask)
}

func absentJSON(raw json.RawMessage) bool {
	return len(raw) == 0 || string(raw) == "null"
}

// The decisions.changed events in this journal are this host's own revision;
// peers' arrive through federation.
func catalogEventIdle(event registry.CatalogEvent) bool {
	return event.Type == "decisions.changed" || catalogProgressTick(event)
}

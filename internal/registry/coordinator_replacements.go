package registry

import (
	"time"
)

// A restart's handover always has a reason; a person's takeover with set --replace has none.
const replacementRows = `FROM coordinator_history WHERE action='replaced' AND previous_session_id!='' AND reason!=''`

// UnannouncedCoordinatorReplacements are handovers whose notices are not all sent yet.
func (r *Registry) UnannouncedCoordinatorReplacements() ([]CoordinatorHistoryEntry, error) {
	rows, err := r.read.Query(`SELECT ` + coordinatorHistoryColumns + ` ` + replacementRows + ` AND announced_at_ms=0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	return appendRows([]CoordinatorHistoryEntry{}, rows, scanCoordinatorHistory)
}

func (r *Registry) MarkCoordinatorReplacementAnnounced(id int64, at time.Time) error {
	_, err := r.db.Exec(`UPDATE coordinator_history SET announced_at_ms=? WHERE id=? AND action='replaced' AND announced_at_ms=0`, at.UnixMilli(), id)
	return err
}

// OpenCoordinatorReplacements are handovers the person has not acknowledged.
func (r *Registry) OpenCoordinatorReplacements() ([]CoordinatorHistoryEntry, error) {
	rows, err := r.read.Query(`SELECT ` + coordinatorHistoryColumns + ` ` + replacementRows + ` AND acknowledged_at_ms=0 ORDER BY id DESC`)
	if err != nil {
		return nil, err
	}
	return appendRows([]CoordinatorHistoryEntry{}, rows, scanCoordinatorHistory)
}

// AcknowledgeCoordinatorReplacement reports false for an id that is not an open handover.
func (r *Registry) AcknowledgeCoordinatorReplacement(id int64, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE coordinator_history SET acknowledged_at_ms=? WHERE id=? AND action='replaced' AND previous_session_id!='' AND reason!='' AND acknowledged_at_ms=0`, at.UnixMilli(), id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// LastCoordinatorReplacement is the latest handover of a project's coordinator.
func (r *Registry) LastCoordinatorReplacement(projectID string) (CoordinatorHistoryEntry, bool, error) {
	rows, err := r.read.Query(`SELECT `+coordinatorHistoryColumns+` `+replacementRows+` AND project_id=? ORDER BY id DESC LIMIT 1`, projectID)
	if err != nil {
		return CoordinatorHistoryEntry{}, false, err
	}
	entries, err := appendRows([]CoordinatorHistoryEntry{}, rows, scanCoordinatorHistory)
	if err != nil || len(entries) == 0 {
		return CoordinatorHistoryEntry{}, false, err
	}
	return entries[0], true, nil
}

// CoordinatorTenure is a span in which a session held a coordinator's name; Until is zero while it still does.
type CoordinatorTenure struct {
	Name  string    `json:"name"`
	From  time.Time `json:"from"`
	Until time.Time `json:"until,omitzero"`
}

// CoordinatorTenures lists when sessionID held a coordinator's name, oldest first.
func (r *Registry) CoordinatorTenures(sessionID string) ([]CoordinatorTenure, error) {
	rows, err := r.read.Query(`SELECT `+coordinatorHistoryColumns+` FROM coordinator_history WHERE session_id=?1 OR previous_session_id=?1 ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	entries, err := appendRows([]CoordinatorHistoryEntry{}, rows, scanCoordinatorHistory)
	if err != nil {
		return nil, err
	}
	var tenures []CoordinatorTenure
	for _, entry := range entries {
		if entry.PreviousSessionID == sessionID && len(tenures) > 0 && tenures[len(tenures)-1].Until.IsZero() {
			tenures[len(tenures)-1].Until = entry.At
		}
		if entry.SessionID == sessionID && entry.Action != "cleared" {
			tenures = append(tenures, CoordinatorTenure{Name: entry.Name, From: entry.At})
		}
	}
	return tenures, nil
}

package daemon

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

// dashboardLeadReplacement is a restart that replaced a coordinator's
// conversation: shown to the person until acknowledged, and on its project.
type dashboardLeadReplacement struct {
	ID                int64     `json:"id"`
	ProjectID         string    `json:"projectId"`
	Name              string    `json:"name"`
	SessionID         string    `json:"sessionId"`
	PreviousSessionID string    `json:"previousSessionId"`
	ChangedBy         string    `json:"changedBy"`
	Reason            string    `json:"reason"`
	At                time.Time `json:"at"`
}

func (d *Daemon) leadReplacementView(entry registry.CoordinatorHistoryEntry) dashboardLeadReplacement {
	return dashboardLeadReplacement{ID: entry.ID, ProjectID: entry.ProjectID, Name: entry.Name, SessionID: d.publicSessionID(entry.SessionID), PreviousSessionID: d.publicSessionID(entry.PreviousSessionID), ChangedBy: d.changedByDisplay(entry.ChangedBy), Reason: replacementWhy(entry), At: entry.At}
}

func (d *Daemon) addLeadReplacements(state *dashboardState) error {
	open, err := d.Registry.OpenCoordinatorReplacements()
	if err != nil {
		return fmt.Errorf("read lead replacements: %w", err)
	}
	state.LeadReplacements = make([]dashboardLeadReplacement, 0, len(open))
	for _, entry := range open {
		state.LeadReplacements = append(state.LeadReplacements, d.leadReplacementView(entry))
	}
	local := d.localHost().ID
	for p := range state.Projects {
		for c := range state.Projects[p].Coordinators {
			if state.Projects[p].Coordinators[c].Host.ID != local {
				continue
			}
			last, found, err := d.Registry.LastCoordinatorReplacement(state.Projects[p].ID)
			if err != nil {
				return fmt.Errorf("read the last lead replacement of %s: %w", state.Projects[p].ID, err)
			}
			if found {
				view := d.leadReplacementView(last)
				state.Projects[p].Coordinators[c].Replaced = &view
			}
		}
	}
	return nil
}

func (d *Daemon) doLeadReplacementAck(_ context.Context, _ principal, request dashboardActionRequest) (actionResult, error) {
	if request.ReplacementID <= 0 {
		return actionResult{}, invalidRequest("replacementId is required")
	}
	acknowledged, err := d.Registry.AcknowledgeCoordinatorReplacement(request.ReplacementID, time.Now())
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	if !acknowledged {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "no open lead replacement has that id")
	}
	d.publishEvent("state.changed", "", map[string]any{"source": "lead-replacement"})
	return actionOK(map[string]any{"ok": true, "acknowledged": request.ReplacementID}), nil
}

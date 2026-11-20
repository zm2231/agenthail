package daemon

import (
	"encoding/json"
	"io"
	"net/http"
)

const notificationPreferenceLeadCompletionsOnly = "leadCompletionsOnly"

type notificationPreference struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Detail  string `json:"detail"`
	Default bool   `json:"default"`
	Value   bool   `json:"value"`
}

// A new preference is one more row here; both apps draw whatever rows the daemon returns.
var notificationPreferences = []notificationPreference{
	{ID: notificationPreferenceLeadCompletionsOnly, Title: "Only the lead's completions in projects with a lead", Detail: "Other agents in a project with a lead stay quiet when they finish. Questions, permission requests and Needs you always come through.", Default: true},
}

func notificationPreferenceRows(config DashboardConfig) []notificationPreference {
	rows := make([]notificationPreference, 0, len(notificationPreferences))
	for _, row := range notificationPreferences {
		row.Value = row.Default
		if value, set := config.NotificationPreferences[row.ID]; set {
			row.Value = value
		}
		rows = append(rows, row)
	}
	return rows
}

func (d *Daemon) notificationPreference(id string) bool {
	config, err := LoadDashboardConfig()
	if err != nil {
		d.log.Printf("read notification preferences: %s", err)
	}
	for _, row := range notificationPreferenceRows(config) {
		if row.ID == id {
			return row.Value
		}
	}
	return false
}

// A finished push from an agent in a project with a lead goes out only from the lead while the preference is on.
func (d *Daemon) completionQuietedForLead(sessionID string) bool {
	if !d.notificationPreference(notificationPreferenceLeadCompletionsOnly) {
		return false
	}
	project := d.storedCatalogProject(sessionID)
	if project == nil {
		return false
	}
	leads, err := d.loadLeadDirectory()
	if err != nil {
		d.log.Printf("read the leads to quiet %s: %s", d.resolveDisplay(sessionID), err)
		return false
	}
	lead, ok := leads.leadOf(project)
	return ok && lead.sessionID != sessionID
}

func (d *Daemon) notificationPreferencesHandler(w http.ResponseWriter, r *http.Request) {
	config, err := LoadDashboardConfig()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	switch r.Method {
	case http.MethodGet:
	case http.MethodPut:
		var request struct {
			ID    string `json:"id"`
			Value *bool  `json:"value"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&request); err != nil || request.Value == nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "send an id and a value")
			return
		}
		known := false
		for _, row := range notificationPreferences {
			known = known || row.ID == request.ID
		}
		if !known {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "unknown notification preference "+request.ID)
			return
		}
		if config.NotificationPreferences == nil {
			config.NotificationPreferences = map[string]bool{}
		}
		config.NotificationPreferences[request.ID] = *request.Value
		if err := SaveDashboardConfig(config); err != nil {
			writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
			return
		}
		d.publishEvent("settings.updated", "", map[string]string{"source": "notification-preferences"})
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"preferences": notificationPreferenceRows(config)})
}

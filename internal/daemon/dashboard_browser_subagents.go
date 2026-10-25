package daemon

import (
	"encoding/json"
	"net/http"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) dashboardBrowserSessionMetadata(w http.ResponseWriter, r *http.Request) {
	captured := &browserResponse{header: http.Header{}}
	d.hostRouted("id", d.dashboardSessionMetadataHandler)(captured, r.Clone(r.Context()))
	if captured.status != http.StatusOK {
		for key, values := range captured.header {
			w.Header()[key] = values
		}
		w.WriteHeader(captured.status)
		_, _ = w.Write(captured.body.Bytes())
		return
	}
	var payload map[string]json.RawMessage
	if json.Unmarshal(captured.body.Bytes(), &payload) != nil {
		writeAPIError(w, 502, "invalid_metadata", "Invalid session metadata")
		return
	}
	if raw, found := payload["claudeSubagents"]; found {
		var links []surface.ClaudeSubagentLink
		var total int
		if json.Unmarshal(raw, &links) != nil || json.Unmarshal(payload["claudeSubagentsTotal"], &total) != nil || total < len(links) {
			writeAPIError(w, 502, "invalid_metadata", "Missing subagent page counts")
			return
		}
		display, working := displayClaudeSubagents(claudeSubagentPage{links: links, total: total})
		payload["claudeSubagents"], _ = json.Marshal(display)
		payload["claudeSubagentsWorking"], _ = json.Marshal(working)
		payload["claudeSubagentsRemaining"], _ = json.Marshal(total - len(links))
	}
	w.Header().Set("Cache-Control", "no-store")
	writeDashboardJSON(w, 200, payload)
}

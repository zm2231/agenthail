package daemon

import (
	"github.com/zm2231/agenthail/internal/surface"
	"net/http"
	"sort"
)

func (d *Daemon) sessionOptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET for session options.", http.StatusMethodNotAllowed)
		return
	}
	type option struct {
		ID        string `json:"id"`
		Workspace bool   `json:"workspace"`
	}
	options := []option{}
	for _, adapter := range d.Surfaces {
		if _, ok := adapter.(surface.SessionStarter); ok {
			options = append(options, option{string(adapter.Name()), true})
		} else if adapter.Name() == surface.KindNotion {
			options = append(options, option{string(adapter.Name()), false})
		}
	}
	sessions, err := d.Registry.ListSessions(200)
	if err != nil {
		http.Error(w, "Saved workspaces are unavailable.", http.StatusInternalServerError)
		return
	}
	directories := []string{}
	seen := map[string]bool{}
	for _, session := range sessions {
		if session.Cwd != "" && !seen[session.Cwd] {
			seen[session.Cwd] = true
			directories = append(directories, session.Cwd)
		}
	}
	sort.Strings(directories)
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	writeDashboardJSON(w, http.StatusOK, map[string]any{"surfaces": options, "workspaces": directories})
}

func (d *Daemon) mobileQueueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET for queued instructions.", http.StatusMethodNotAllowed)
		return
	}
	rows, err := d.Registry.ListQueue(true)
	if err != nil {
		http.Error(w, "Queued instructions are unavailable.", http.StatusInternalServerError)
		return
	}
	items := []dashboardQueue{}
	for _, item := range rows {
		if item.Status != "pending" && item.Status != "inflight" && item.Status != "dead" && item.Status != "expired" {
			continue
		}
		items = append(items, dashboardQueue{TurnOptions: item.TurnOptions, SourceSessionID: item.SourceSessionID, ID: item.ID, SessionID: item.SessionID, Target: d.resolveDisplay(item.SessionID), Message: item.Message, Model: item.Model, Status: item.Status, Attempts: item.Attempts, LastError: item.LastError, QueuedAt: item.QueuedAt})
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items})
}

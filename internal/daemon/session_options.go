package daemon

import (
	"net/http"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// sessionOptionsHandler is this host's launch catalog: the agents it can
// start, whether each is connected, its launchers, and the folders its own
// sessions ran in. A request naming a paired host is answered by that host.
func (d *Daemon) sessionOptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET for session options.", http.StatusMethodNotAllowed)
		return
	}
	type option struct {
		ID        string `json:"id"`
		Workspace bool   `json:"workspace"`
		Connected bool   `json:"connected"`
	}
	health := map[surface.SurfaceKind]string{}
	if states, err := d.Registry.CatalogSurfaces(); err == nil {
		for _, state := range states {
			health[state.Surface] = state.Health
		}
	}
	options := []option{}
	for _, adapter := range d.Surfaces {
		connected := health[adapter.Name()] == "healthy"
		if _, ok := adapter.(surface.SessionStarter); ok {
			options = append(options, option{string(adapter.Name()), true, connected})
		} else if adapter.Name() == surface.KindNotion {
			options = append(options, option{string(adapter.Name()), false, connected})
		}
	}
	sessions, err := d.Registry.ListSessions(200)
	if err != nil {
		http.Error(w, "Saved workspaces are unavailable.", http.StatusInternalServerError)
		return
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	launchers := []map[string]any{}
	if d.transportResolver != nil {
		launchers = d.transportResolver.Options(r.Context())
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost(), "surfaces": options, "workspaces": launchWorkspaces(sessions), "launchers": launchers})
}

const launchWorkspaceLimit = 12

// launchWorkspaces lists the folders of the newest sessions first, leaving
// out scratch and temporary directories nobody starts work in.
func launchWorkspaces(sessions []surface.Session) []string {
	directories := []string{}
	seen := map[string]bool{}
	for _, session := range sessions {
		cwd := session.Cwd
		if cwd == "" || seen[cwd] || strings.HasPrefix(cwd, "/private/") || strings.HasPrefix(cwd, "/var/") || strings.HasPrefix(cwd, "/tmp/") || strings.Contains(cwd, "/.no-mistakes/") {
			continue
		}
		seen[cwd] = true
		directories = append(directories, cwd)
		if len(directories) == launchWorkspaceLimit {
			break
		}
	}
	return directories
}

func (d *Daemon) mobileQueueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Use GET for queued instructions.", http.StatusMethodNotAllowed)
		return
	}
	rows, err := d.Registry.ListQueue(true, d.adapters())
	if err != nil {
		http.Error(w, "Queued instructions are unavailable.", http.StatusInternalServerError)
		return
	}
	items := []dashboardQueue{}
	historyCount := 0
	for index := len(rows) - 1; index >= 0; index-- {
		item := rows[index]
		if item.Historical {
			if historyCount >= 100 {
				continue
			}
			historyCount++
		}
		items = append(items, d.dashboardQueueItem(item))
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	writeDashboardJSON(w, http.StatusOK, map[string]any{"items": items})
}

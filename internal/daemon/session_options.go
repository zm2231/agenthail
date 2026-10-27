package daemon

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

// sessionOptionsHandler is this host's launch catalog: the agents it can
// start, whether each is connected, its launchers, its projects, and the
// folders of its sessions outside any project. A
// request naming a paired host is answered by that host.
func (d *Daemon) sessionOptionsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for session options.")
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
	projects, err := d.launchProjects()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Projects are unavailable.")
		return
	}
	folders, err := d.launchFolders()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Folders are unavailable.")
		return
	}
	sort.Slice(options, func(i, j int) bool { return options[i].ID < options[j].ID })
	launchers := []map[string]any{}
	if d.transportResolver != nil {
		launchers = d.transportResolver.Options(r.Context())
	}
	claudeConfigRoots := append([]surface.ClaudeConfigRoot{}, d.claudeConfigRoots()...)
	if home, err := os.UserHomeDir(); err == nil {
		for i := range claudeConfigRoots {
			claudeConfigRoots[i].Login = surface.ClaudeRootLogin(home, claudeConfigRoots[i])
		}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"host": d.localHost(), "surfaces": options, "projects": projects, "folders": folders, "launchers": launchers, "claudeConfigRoots": claudeConfigRoots})
}

const launchProjectLimit = 12

// launchProject is a project of this host a new session can start in: an
// added folder, or a repository's main checkout.
type launchProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
	Kind        string `json:"kind"`
	Added       bool   `json:"added"`
}

// launchProjects lists this host's projects, the most recently active first,
// followed by added projects and coordinators' projects without sessions.
// Archived projects are left out.
func (d *Daemon) launchProjects() ([]launchProject, error) {
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	archive, err := d.Registry.ArchiveState()
	if err != nil {
		return nil, err
	}
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, err
	}
	type candidate struct {
		project    launchProject
		lastActive time.Time
	}
	byID := map[string]*candidate{}
	for _, record := range snapshot.Sessions {
		if record.HostID != "" || record.Session.Subagent != nil {
			continue
		}
		var project CatalogProject
		if json.Unmarshal(record.Project, &project) != nil || project.LocalID == "" || project.RootPath == "" {
			continue
		}
		if _, archived := archive.Projects[project.LocalID]; archived {
			continue
		}
		entry, found := byID[project.LocalID]
		if !found {
			entry = &candidate{project: launchProject{ID: project.LocalID, DisplayName: project.DisplayName, RootPath: project.RootPath, Kind: project.Kind, Added: project.Added}}
			byID[project.LocalID] = entry
		}
		if record.Session.LastActive.After(entry.lastActive) {
			entry.lastActive = record.Session.LastActive
		}
	}
	for _, record := range records {
		if _, found := byID[record.ID]; found || !record.Added || !record.ArchivedAt.IsZero() {
			continue
		}
		added := addedCatalogProject(record, false)
		byID[record.ID] = &candidate{project: launchProject{ID: added.LocalID, DisplayName: added.DisplayName, RootPath: added.RootPath, Kind: added.Kind, Added: true}}
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return nil, err
	}
	listed := make([]registry.ProjectRecord, 0, len(byID))
	for _, entry := range byID {
		listed = append(listed, registry.ProjectRecord{WorkspaceRoot: entry.project.RootPath})
	}
	for _, spec := range coordinators {
		if projectRootHeld(records, listed, spec) {
			continue
		}
		found := coordinatorCatalogProject(spec)
		if _, archived := archive.Projects[found.LocalID]; archived {
			continue
		}
		byID[found.LocalID] = &candidate{project: launchProject{ID: found.LocalID, DisplayName: found.DisplayName, RootPath: found.RootPath, Kind: found.Kind}}
	}
	candidates := make([]*candidate, 0, len(byID))
	for _, entry := range byID {
		candidates = append(candidates, entry)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if !candidates[i].lastActive.Equal(candidates[j].lastActive) {
			return candidates[i].lastActive.After(candidates[j].lastActive)
		}
		return candidates[i].project.DisplayName < candidates[j].project.DisplayName
	})
	if len(candidates) > launchProjectLimit {
		candidates = candidates[:launchProjectLimit]
	}
	projects := make([]launchProject, 0, len(candidates))
	for _, entry := range candidates {
		projects = append(projects, entry.project)
	}
	return projects, nil
}

// launchFolders lists the working folders of this host's recent sessions
// that no project holds, the most recent first, so a person can add one as a
// project. It names only folders sessions ran in.
func (d *Daemon) launchFolders() ([]string, error) {
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return nil, err
	}
	archive, err := d.Registry.ArchiveState()
	if err != nil {
		return nil, err
	}
	lastActive := map[string]time.Time{}
	for _, record := range snapshot.Sessions {
		cwd := record.Session.Cwd
		if record.HostID != "" || record.Session.Subagent != nil || cwd == "" || !filepath.IsAbs(cwd) {
			continue
		}
		if _, archived := archive.Sessions[record.Session.ID]; archived {
			continue
		}
		var project *CatalogProject
		if json.Unmarshal(record.Project, &project) == nil && project != nil && project.ID != "" {
			continue
		}
		if record.Session.LastActive.After(lastActive[cwd]) || lastActive[cwd].IsZero() {
			lastActive[cwd] = record.Session.LastActive
		}
	}
	folders := make([]string, 0, len(lastActive))
	for folder := range lastActive {
		folders = append(folders, folder)
	}
	sort.Slice(folders, func(i, j int) bool {
		if !lastActive[folders[i]].Equal(lastActive[folders[j]]) {
			return lastActive[folders[i]].After(lastActive[folders[j]])
		}
		return folders[i] < folders[j]
	})
	if len(folders) > launchProjectLimit {
		folders = folders[:launchProjectLimit]
	}
	return folders, nil
}

func (d *Daemon) mobileQueueHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for queued instructions.")
		return
	}
	rows, err := d.Registry.ListQueue(true, d.adapters())
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", "Queued instructions are unavailable.")
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

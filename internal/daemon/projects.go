package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zm2231/agenthail/internal/project"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

type catalogViewState struct {
	archive   registry.ArchiveState
	attention map[string]bool
}

func (d *Daemon) loadCatalogViewState() (catalogViewState, error) {
	archive, err := d.Registry.ArchiveState()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read archive state: %w", err)
	}
	items, err := d.Registry.ListAttentionItems(false)
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read attention items: %w", err)
	}
	attention := make(map[string]bool, len(items))
	for _, item := range items {
		attention[item.SessionID] = true
	}
	return catalogViewState{archive: archive, attention: attention}, nil
}

// settleArchiveActivity unarchives each archived session that shows activity
// (a turn after the archive, work in progress, a queued item, or an open
// attention item) together with its project, so nothing that needs the user
// stays hidden. It runs over every row before any row's flags are set, so one
// session's activity reaches all rows of its project. It reports whether a
// project was unarchived.
func (d *Daemon) settleArchiveActivity(rows []dashboardSession, view catalogViewState) bool {
	projectCleared := false
	for _, row := range rows {
		projectID := ""
		if row.Project != nil {
			projectID = row.Project.ID
		}
		needsYou := row.Status == surface.StatusBusy || row.QueueCount > 0 || view.attention[row.ID]
		sessionAt, sessionArchived := view.archive.Sessions[row.ID]
		projectAt, projectArchived := view.archive.Projects[projectID]
		sessionActive := sessionArchived && (needsYou || row.LastActive.After(sessionAt))
		projectActive := projectArchived && (needsYou || row.LastActive.After(projectAt))
		if !sessionActive && !projectActive {
			continue
		}
		clearProject := ""
		if projectActive {
			clearProject = projectID
		}
		if err := d.Registry.ClearArchiveForActivity(row.ID, clearProject); err != nil {
			d.log.Printf("unarchive active session %s: %s", d.resolveDisplay(row.ID), err)
			continue
		}
		delete(view.archive.Sessions, row.ID)
		if projectActive {
			delete(view.archive.Projects, projectID)
			projectCleared = true
		}
	}
	return projectCleared
}

// applyCatalogView sets the row's archive flags. Presence (current) stays as
// the surface computed it.
func applyCatalogView(row *dashboardSession, view catalogViewState) {
	_, sessionArchived := view.archive.Sessions[row.ID]
	projectArchived := false
	if row.Project != nil {
		rowProject := *row.Project
		_, projectArchived = view.archive.Projects[rowProject.ID]
		rowProject.Archived = projectArchived
		row.Project = &rowProject
	}
	row.Archived = sessionArchived || projectArchived
}

type dashboardProject struct {
	ID           string         `json:"id"`
	DisplayName  string         `json:"displayName"`
	RootPath     string         `json:"rootPath"`
	Kind         string         `json:"kind"`
	Host         *dashboardHost `json:"host,omitempty"`
	LastActive   time.Time      `json:"lastActive,omitempty"`
	SessionCount int            `json:"sessionCount"`
	Archived     bool           `json:"archived"`
}

// catalogScopes lists the session scopes every client renders. A scope counts
// session families: a subagent belongs to its root's family when the root is
// listed.
var catalogScopes = []string{"running", "current", "recent", "all", "archived"}

func validCatalogScope(scope string) bool {
	if scope == "" {
		return true
	}
	for _, known := range catalogScopes {
		if scope == known {
			return true
		}
	}
	return false
}

func catalogScopeMatches(scope string, row dashboardSession) bool {
	return ScopeMatches(scope, SessionWorking(row.Status, row.Subagents), row.Current, row.Archived)
}

// SessionWorking reports whether a session or one of its observed subagents is
// working.
func SessionWorking(status surface.SessionStatus, subagents *surface.SubagentRollup) bool {
	return status == surface.StatusBusy || subagents != nil && subagents.Working > 0
}

// ScopeMatches is the one definition of the session scopes, applied to the
// flags the owning daemon computed for a row. An empty scope matches every
// row.
func ScopeMatches(scope string, working, current, archived bool) bool {
	switch scope {
	case "running":
		return !archived && working
	case "current", "recent":
		return !archived && current
	case "all":
		return !archived
	case "archived":
		return archived
	default:
		return true
	}
}

func catalogScopeCounts(rows []dashboardSession) map[string]int {
	listed := make(map[string]bool, len(rows))
	for _, row := range rows {
		listed[row.ID] = true
	}
	family := func(row dashboardSession) string {
		if row.Subagent != nil && row.Subagent.RootID != "" && listed[row.Subagent.RootID] {
			return row.Subagent.RootID
		}
		return row.ID
	}
	counts := make(map[string]int, len(catalogScopes))
	for _, scope := range catalogScopes {
		matched := map[string]bool{}
		for _, row := range rows {
			if catalogScopeMatches(scope, row) {
				matched[family(row)] = true
			}
		}
		counts[scope] = len(matched)
	}
	return counts
}

// catalogProjects adds registered folder projects that have no sessions yet.
func catalogProjects(rows []dashboardSession, records []registry.ProjectRecord, local dashboardHost) []dashboardProject {
	byID := map[string]*dashboardProject{}
	order := []string{}
	for _, row := range rows {
		if row.Project == nil || row.Project.ID == "" {
			continue
		}
		project, found := byID[row.Project.ID]
		if !found {
			project = &dashboardProject{ID: row.Project.ID, DisplayName: row.Project.DisplayName, RootPath: row.Project.RootPath, Kind: row.Project.Kind, Host: row.Host, Archived: row.Project.Archived}
			byID[row.Project.ID] = project
			order = append(order, row.Project.ID)
		}
		if row.Archived || (row.Subagent != nil && row.Subagent.ParentID != "") {
			continue
		}
		project.SessionCount++
		if row.LastActive.After(project.LastActive) {
			project.LastActive = row.LastActive
		}
	}
	for _, record := range records {
		if !record.Registered {
			continue
		}
		if _, found := byID[record.ID]; found {
			continue
		}
		host := local
		byID[record.ID] = &dashboardProject{ID: record.ID, DisplayName: record.DisplayName, RootPath: record.RootPath, Kind: record.Kind, Host: &host, Archived: !record.ArchivedAt.IsZero()}
		order = append(order, record.ID)
	}
	projects := make([]dashboardProject, 0, len(order))
	for _, id := range order {
		projects = append(projects, *byID[id])
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].LastActive.After(projects[j].LastActive) })
	return projects
}

func (d *Daemon) requestCatalogDiscovery() {
	select {
	case d.catalogNudge <- struct{}{}:
	default:
	}
}

func (d *Daemon) projectAction(ctx context.Context, w http.ResponseWriter, action, projectID, path string) {
	switch action {
	case "project-add":
		project, err := d.addProject(ctx, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "project": project})
		return
	case "project-archive", "project-unarchive":
	default:
		http.Error(w, "unknown project action", http.StatusBadRequest)
		return
	}
	if projectID == "" {
		http.Error(w, "projectId is required", http.StatusBadRequest)
		return
	}
	var err error
	if action == "project-archive" {
		var record registry.ProjectRecord
		record, err = d.catalogProjectRecord(projectID)
		if errors.Is(err, sql.ErrNoRows) {
			http.Error(w, "project not found", http.StatusNotFound)
			return
		}
		if err == nil {
			err = d.Registry.ArchiveProject(record, time.Now().UTC())
		}
	} else {
		err = d.Registry.UnarchiveProject(projectID)
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.publishCatalogPresence()
	// A catalog pass that read the archive state before this change may still
	// publish its rows; the pass this requests runs after it and settles them.
	d.requestCatalogDiscovery()
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "projectId": projectID, "archived": action == "project-archive"})
}

func (d *Daemon) sessionArchiveAction(w http.ResponseWriter, action, sessionID string) {
	var err error
	if action == "session-archive" {
		err = d.Registry.ArchiveSession(sessionID, time.Now().UTC())
	} else {
		err = d.Registry.UnarchiveSession(sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.publishCatalogPresence()
	// A catalog pass that read the archive state before this change may still
	// publish its rows; the pass this requests runs after it and settles them.
	d.requestCatalogDiscovery()
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "sessionId": sessionID, "archived": action == "session-archive"})
}

func (d *Daemon) catalogProjectRecord(projectID string) (registry.ProjectRecord, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return registry.ProjectRecord{}, err
	}
	for _, record := range records {
		if record.ID == projectID {
			return record, nil
		}
	}
	raw, err := d.Registry.CatalogProjectByID(projectID)
	if err != nil {
		return registry.ProjectRecord{}, err
	}
	var stored project.Project
	if err := json.Unmarshal(raw, &stored); err != nil {
		return registry.ProjectRecord{}, err
	}
	return registry.ProjectRecord{ID: stored.ID, Kind: stored.Kind, RootPath: stored.RootPath, DisplayName: stored.DisplayName}, nil
}

// addProject makes a folder a project. A folder inside a Git repository is
// already part of that repository's project and is returned unchanged.
func (d *Daemon) addProject(ctx context.Context, path string) (project.Project, error) {
	path, err := workspace.NormalizeCWD(path)
	if err != nil || path == "" {
		return project.Project{}, fmt.Errorf("a folder path is required")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return project.Project{}, fmt.Errorf("%s is not a folder", path)
	}
	repository, notRepository, err := project.ReadRepository(ctx, path)
	if err == nil {
		return *repository.Project(), nil
	}
	if !notRepository {
		return project.Project{}, fmt.Errorf("read repository for %s: %w", path, err)
	}
	folder := project.Project{ID: project.PathID("project", path), DisplayName: filepath.Base(path), RootPath: path, Kind: project.KindFolder}
	if err := d.Registry.RegisterFolderProject(folder.ID, folder.RootPath, folder.DisplayName); err != nil {
		return project.Project{}, err
	}
	d.requestCatalogDiscovery()
	return folder, nil
}

package daemon

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
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
			projectID = row.Project.LocalID
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
		project := *row.Project
		_, projectArchived = view.archive.Projects[project.LocalID]
		project.Archived = projectArchived
		row.Project = &project
	}
	row.Archived = sessionArchived || projectArchived
}

// dashboardProject is one project across every host: each checkout is one
// host's project with the same key, or the host's only checkout of a project
// without one. It is archived when every checkout is. Coordinators lists the
// project's coordinator on each host that has one; more than one is a
// conflict.
type dashboardProject struct {
	ID           string                        `json:"id"`
	DisplayName  string                        `json:"displayName"`
	Kind         string                        `json:"kind"`
	Key          string                        `json:"key,omitempty"`
	Repository   *project.Repository           `json:"repository,omitempty"`
	Checkouts    []dashboardProjectCheckout    `json:"checkouts"`
	Coordinators []dashboardProjectCoordinator `json:"coordinators"`
	LastActive   time.Time                     `json:"lastActive,omitempty"`
	SessionCount int                           `json:"sessionCount"`
	Archived     bool                          `json:"archived"`
}

// dashboardProjectCoordinator is one host's coordinator of a project.
// SessionID is empty until its first message starts it.
type dashboardProjectCoordinator struct {
	Host      dashboardHost `json:"host"`
	Name      string        `json:"name"`
	Surface   string        `json:"surface"`
	SessionID string        `json:"sessionId,omitempty"`
	ChangedBy string        `json:"changedBy,omitempty"`
	ChangedAt time.Time     `json:"changedAt"`
}

type dashboardProjectCheckout struct {
	Host         dashboardHost `json:"host"`
	ProjectID    string        `json:"projectId"`
	RootPath     string        `json:"rootPath"`
	Added        bool          `json:"added"`
	SessionCount int           `json:"sessionCount"`
	Archived     bool          `json:"archived"`
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

// catalogProjects groups the rows by project, adds the added projects and this
// host's coordinators' projects that have no sessions yet, and names each
// project's coordinators. A paired host's coordinator is listed on a project
// this host shows.
func catalogProjects(rows []dashboardSession, records []registry.ProjectRecord, coordinators []registry.CoordinatorSpec, archived map[string]time.Time, local dashboardHost, hostRef func(string) dashboardHost) []dashboardProject {
	byID := map[string]*dashboardProject{}
	order := []string{}
	entry := func(found *CatalogProject) *dashboardProject {
		project, ok := byID[found.ID]
		if !ok {
			project = &dashboardProject{ID: found.ID, DisplayName: found.DisplayName, Kind: found.Kind, Key: found.Key, Repository: found.Repository}
			byID[found.ID] = project
			order = append(order, found.ID)
		}
		return project
	}
	checkout := func(project *dashboardProject, host dashboardHost, found *CatalogProject) *dashboardProjectCheckout {
		for index := range project.Checkouts {
			if project.Checkouts[index].Host.ID == host.ID && project.Checkouts[index].ProjectID == found.LocalID {
				return &project.Checkouts[index]
			}
		}
		project.Checkouts = append(project.Checkouts, dashboardProjectCheckout{Host: host, ProjectID: found.LocalID, RootPath: found.RootPath, Added: found.Added, Archived: found.Archived})
		return &project.Checkouts[len(project.Checkouts)-1]
	}
	for _, row := range rows {
		if row.Project == nil || row.Project.ID == "" {
			continue
		}
		host := local
		if row.Host != nil {
			host = *row.Host
		}
		project := entry(row.Project)
		at := checkout(project, host, row.Project)
		if row.Archived || (row.Subagent != nil && row.Subagent.ParentID != "") {
			continue
		}
		project.SessionCount++
		at.SessionCount++
		if row.LastActive.After(project.LastActive) {
			project.LastActive = row.LastActive
		}
	}
	for _, record := range records {
		if !record.Added {
			continue
		}
		found := addedCatalogProject(record, false)
		found.Archived = !record.ArchivedAt.IsZero()
		project := entry(found)
		checkout(project, local, found)
	}
	for _, spec := range coordinators {
		if spec.HostID != "" {
			continue
		}
		if _, listed := byID[spec.ProjectID]; listed {
			continue
		}
		found := coordinatorCatalogProject(spec)
		_, found.Archived = archived[found.LocalID]
		checkout(entry(found), local, found)
	}
	for _, spec := range coordinators {
		project, listed := byID[spec.ProjectID]
		if !listed {
			continue
		}
		host := local
		if spec.HostID != "" {
			host = hostRef(spec.HostID)
		}
		project.Coordinators = append(project.Coordinators, dashboardProjectCoordinator{Host: host, Name: spec.Name, Surface: spec.Surface, SessionID: spec.SessionID, ChangedBy: spec.ChangedBy, ChangedAt: spec.ChangedAt})
	}
	projects := make([]dashboardProject, 0, len(order))
	for _, id := range order {
		project := byID[id]
		if project.Coordinators == nil {
			project.Coordinators = []dashboardProjectCoordinator{}
		}
		sort.SliceStable(project.Coordinators, func(i, j int) bool {
			left, right := project.Coordinators[i], project.Coordinators[j]
			if (left.Host.ID == local.ID) != (right.Host.ID == local.ID) {
				return left.Host.ID == local.ID
			}
			return left.Host.Name < right.Host.Name
		})
		project.Archived = true
		for _, at := range project.Checkouts {
			project.Archived = project.Archived && at.Archived
		}
		sort.SliceStable(project.Checkouts, func(i, j int) bool {
			left, right := project.Checkouts[i], project.Checkouts[j]
			if (left.Host.ID == local.ID) != (right.Host.ID == local.ID) {
				return left.Host.ID == local.ID
			}
			if left.Host.Name != right.Host.Name {
				return left.Host.Name < right.Host.Name
			}
			return left.RootPath < right.RootPath
		})
		projects = append(projects, *project)
	}
	sort.SliceStable(projects, func(i, j int) bool { return projects[i].LastActive.After(projects[j].LastActive) })
	return projects
}

// coordinatorCatalogProject is the project of this host's coordinator spec
// when no session or added project names it. Added projects are always
// listed, so it is a repository's project, rooted at its main checkout.
func coordinatorCatalogProject(spec registry.CoordinatorSpec) *CatalogProject {
	found := &CatalogProject{ID: spec.ProjectID, LocalID: catalogImplicitProjectID(spec.ProjectRoot), DisplayName: catalogProjectName(spec.ProjectRoot), RootPath: spec.ProjectRoot, Kind: catalogProjectGit}
	if found.ID != found.LocalID {
		found.Key = found.ID
	}
	return found
}

// coordinatorHostRef names a paired host for a mirrored coordinator. A host
// removed since keeps its id as its name until its rows are dropped.
func (d *Daemon) coordinatorHostRef(hostID string) dashboardHost {
	if host, found := d.pairedHost(hostID); found {
		return host.ref()
	}
	return dashboardHost{ID: hostID, Name: hostID, State: hostStateUnreachable}
}

func (d *Daemon) requestCatalogDiscovery() {
	select {
	case d.catalogNudge <- struct{}{}:
	default:
	}
}

func (d *Daemon) projectAction(r *http.Request, w http.ResponseWriter, request projectRequest) {
	ctx := r.Context()
	switch request.Action {
	case "project-add":
		project, err := d.addProject(ctx, request.Path, request.Title)
		var taken *registry.WorkspaceTakenError
		if errors.As(err, &taken) {
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "project": project})
		return
	case "project-remove", "project-archive", "project-unarchive":
	default:
		http.Error(w, "unknown project action", http.StatusBadRequest)
		return
	}
	if request.ProjectID == "" {
		http.Error(w, "projectId is required", http.StatusBadRequest)
		return
	}
	targets, err := d.localProjectCheckouts(request.ProjectID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if request.Action == "project-remove" {
		d.removeProject(w, request.ProjectID, targets)
		return
	}
	archive := request.Action == "project-archive"
	var checkouts []map[string]any
	failed := false
	for _, target := range targets {
		result := map[string]any{"host": d.localHost().Name, "projectId": target.ID, "rootPath": target.WorkspaceRoot, "ok": true}
		if err := d.archiveLocalProject(target, archive); err != nil {
			result["ok"], result["error"] = false, err.Error()
			failed = true
		}
		checkouts = append(checkouts, result)
	}
	if len(targets) > 0 {
		d.publishCatalogPresence()
		// A catalog pass that read the archive state before this change may
		// still publish its rows; the pass this requests runs after it and
		// settles them.
		d.requestCatalogDiscovery()
	}
	// A paired host forwarding this action archives only its own checkouts,
	// so the action never travels back.
	if _, fromHost := requestingHost(ctx); !fromHost {
		hostIDs, err := d.Registry.CatalogProjectHosts(request.ProjectID)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		for _, hostID := range hostIDs {
			result := map[string]any{"host": hostID, "ok": true}
			if host, found := d.pairedHost(hostID); found {
				result["host"] = host.record.Name
				if err := d.forwardProjectArchive(r, host, request.Action, request.ProjectID); err != nil {
					result["ok"], result["error"] = false, err.Error()
					failed = true
				}
			} else {
				result["ok"], result["error"] = false, "host is no longer paired"
				failed = true
			}
			checkouts = append(checkouts, result)
		}
	}
	if len(checkouts) == 0 {
		http.Error(w, "project not found", http.StatusNotFound)
		return
	}
	status := http.StatusOK
	if failed {
		status = http.StatusBadGateway
	}
	writeDashboardJSON(w, status, map[string]any{"ok": !failed, "projectId": request.ProjectID, "archived": archive, "checkouts": checkouts})
}

type projectRequest struct {
	Action    string
	ProjectID string
	Path      string
	Title     string
}

// localProjectCheckouts lists this host's projects a project id names: the
// project with that local id, or every one with that key.
func (d *Daemon) localProjectCheckouts(projectID string) ([]registry.ProjectRecord, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var targets []registry.ProjectRecord
	for _, record := range records {
		if record.ID == projectID || (record.Added && record.Key == projectID) {
			targets = append(targets, record)
			seen[record.ID] = true
		}
	}
	raws, err := d.Registry.CatalogLocalProjects(projectID)
	if err != nil {
		return nil, err
	}
	for _, raw := range raws {
		var found CatalogProject
		if json.Unmarshal(raw, &found) != nil || found.LocalID == "" || seen[found.LocalID] {
			continue
		}
		seen[found.LocalID] = true
		targets = append(targets, registry.ProjectRecord{ID: found.LocalID, WorkspaceRoot: found.RootPath, Title: found.DisplayName, Added: found.Added})
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return nil, err
	}
	for _, spec := range coordinators {
		found := coordinatorCatalogProject(spec)
		if found.ID != projectID && found.LocalID != projectID || seen[found.LocalID] || addedProjectNamed(records, spec.ProjectID) {
			continue
		}
		seen[found.LocalID] = true
		targets = append(targets, registry.ProjectRecord{ID: found.LocalID, WorkspaceRoot: found.RootPath, Title: found.DisplayName})
	}
	return targets, nil
}

// addedProjectNamed reports whether an added project is the project with this
// id, by its own id or its repository key.
func addedProjectNamed(records []registry.ProjectRecord, id string) bool {
	for _, record := range records {
		if record.Added && (record.ID == id || record.Key == id) {
			return true
		}
	}
	return false
}

func (d *Daemon) archiveLocalProject(target registry.ProjectRecord, archive bool) error {
	if archive {
		return d.Registry.ArchiveProject(target, time.Now().UTC())
	}
	return d.Registry.UnarchiveProject(target.ID)
}

// removeProject removes one added project of this host. Its sessions move to
// the next project that contains them on the following catalog pass.
func (d *Daemon) removeProject(w http.ResponseWriter, projectID string, targets []registry.ProjectRecord) {
	var added []registry.ProjectRecord
	for _, target := range targets {
		if target.Added {
			added = append(added, target)
		}
	}
	switch len(added) {
	case 0:
		if len(targets) > 0 {
			http.Error(w, "project "+projectID+" was not added, so it cannot be removed; archive it to hide it", http.StatusConflict)
			return
		}
		http.Error(w, "project not found", http.StatusNotFound)
		return
	case 1:
	default:
		roots := make([]string, 0, len(added))
		for _, target := range added {
			roots = append(roots, target.ID+" ("+target.WorkspaceRoot+")")
		}
		http.Error(w, "project "+projectID+" names several added folders on this host: "+strings.Join(roots, ", ")+"; remove one by its id", http.StatusConflict)
		return
	}
	if err := d.Registry.RemoveProject(added[0].ID); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	d.requestCatalogDiscovery()
	writeDashboardJSON(w, http.StatusOK, map[string]any{"ok": true, "projectId": added[0].ID, "rootPath": added[0].WorkspaceRoot})
}

func (d *Daemon) forwardProjectArchive(r *http.Request, host *pairedHost, action, projectID string) error {
	body, err := json.Marshal(map[string]string{"action": action, "projectId": projectID})
	if err != nil {
		return err
	}
	key := r.Header.Get(actionIdempotencyHeader)
	if key == "" {
		key = "fwd-" + uuid.NewString()
	}
	header := http.Header{actionIdempotencyHeader: []string{key + ":" + host.record.ID}}
	response, err := host.client.Do(r.Context(), http.MethodPost, "/api/v1/actions", nil, body, header)
	if err != nil {
		return err
	}
	answer, err := readHostAnswer(host, response)
	if err != nil {
		return err
	}
	if answer.status != http.StatusOK {
		return fmt.Errorf("%s answered %d: %s", host.record.Name, answer.status, strings.TrimSpace(string(answer.payload)))
	}
	return nil
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

// addProject adds a folder as a project of this host. A folder in a linked
// worktree is added at the same place in the main checkout, so the project
// holds that folder in every worktree.
func (d *Daemon) addProject(ctx context.Context, path, title string) (CatalogProject, error) {
	path, err := workspace.NormalizeCWD(path)
	if err != nil || path == "" {
		return CatalogProject{}, fmt.Errorf("a folder path is required")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return CatalogProject{}, fmt.Errorf("%s is not a folder", path)
	}
	if path, err = canonicalCatalogPath(path); err != nil {
		return CatalogProject{}, err
	}
	record := registry.ProjectRecord{ID: "p-" + uuid.NewString(), WorkspaceRoot: path, Title: strings.TrimSpace(title), CreatedAt: time.Now().UTC()}
	repository, notRepository, err := resolveCatalogRepository(ctx, path, nil)
	inRepository := err == nil
	switch {
	case inRepository:
		record.WorkspaceRoot = repository.translate(path)
		if record.Key = repository.key(record.WorkspaceRoot); record.Key != "" {
			record.Repository, _ = json.Marshal(repository.repository)
		}
	case !notRepository:
		return CatalogProject{}, fmt.Errorf("read repository for %s: %w", path, err)
	}
	if record.Title == "" {
		record.Title = filepath.Base(record.WorkspaceRoot)
	}
	if err := d.Registry.AddProject(record); err != nil {
		return CatalogProject{}, err
	}
	d.requestCatalogDiscovery()
	return *addedCatalogProject(record, inRepository), nil
}

// ResolvePathProject finds the project a session working in path belongs
// to, resolved the way the catalog resolves sessions. It is nil when no
// project holds the folder.
func ResolvePathProject(ctx context.Context, reg *registry.Registry, path string) (*CatalogProject, error) {
	path, err := catalogSessionPath(path)
	if err != nil || path == "" {
		return nil, fmt.Errorf("a folder path is required")
	}
	records, err := reg.ListProjects()
	if err != nil {
		return nil, err
	}
	added := records[:0]
	for _, record := range records {
		if record.Added {
			added = append(added, record)
		}
	}
	identity, _, transient := resolveCatalogIdentity(ctx, path, added, nil)
	if transient {
		return nil, fmt.Errorf("resolve the project of %s: %s", path, identity.UnavailableReason)
	}
	return identity.Project, nil
}

// refreshProjectRepositories keeps each added project's key current with its
// repository's remote.
func (d *Daemon) refreshProjectRepositories(ctx context.Context, records []registry.ProjectRecord, now time.Time) {
	for index := range records {
		record := &records[index]
		if !record.Added {
			continue
		}
		key, repository, ok := d.catalogIdentities.rootRepository(ctx, record.WorkspaceRoot, now)
		if !ok || (key == record.Key && bytes.Equal(repository, record.Repository)) {
			continue
		}
		if err := d.Registry.SetProjectRepository(record.ID, key, repository); err != nil {
			d.log.Printf("project %s repository: %s", record.WorkspaceRoot, err)
			continue
		}
		record.Key, record.Repository = key, repository
	}
}

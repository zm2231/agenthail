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
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/project"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/workspace"
)

type catalogViewState struct {
	archive   registry.ArchiveState
	attention map[string]bool
	marks     map[string]registry.SessionMark
	reads     map[string]surface.ReadState
	turnEnds  map[string]registry.TurnEnd
	asks      map[string]registry.Ask
	notices   map[string]registry.TurnNotice
	judgments map[string]registry.TurnJudgment
	leads     leadDirectory
	held      map[string][]heldWorkRow
}

func (d *Daemon) buildCatalogViewState() (catalogViewState, error) {
	archive, err := d.Registry.ArchiveState()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read archive state: %w", err)
	}
	open, err := d.Registry.OpenAsks()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read open asks: %w", err)
	}
	attention := make(map[string]bool, len(open))
	for _, ask := range open {
		if registry.AskWaitsOnPerson(ask) {
			attention[ask.SessionID] = true
		}
	}
	marks, err := d.Registry.SessionMarks()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read pinned and snoozed sessions: %w", err)
	}
	reads, err := d.Registry.CodexReadStates()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read read states: %w", err)
	}
	turnEnds, err := d.Registry.CurrentTurnEnds()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read turn ends: %w", err)
	}
	asks, err := d.Registry.RowAsks()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read asks: %w", err)
	}
	notices, err := d.Registry.TurnNotices()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read turn notices: %w", err)
	}
	judgments, err := d.Registry.CurrentTurnJudgments()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read turn judgments: %w", err)
	}
	leads, err := d.loadLeadDirectory()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read project leads: %w", err)
	}
	held, err := d.heldWorkBySession()
	if err != nil {
		return catalogViewState{}, fmt.Errorf("read held work: %w", err)
	}
	return catalogViewState{archive: archive, attention: attention, marks: marks, reads: reads, turnEnds: turnEnds, asks: asks, notices: notices, judgments: judgments, leads: leads, held: held}, nil
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
	archived, projectArchived := catalogViewArchive(row, view)
	if row.Project != nil {
		project := *row.Project
		project.Archived = projectArchived
		row.Project = &project
	}
	row.Archived = archived
	mark := view.marks[row.ID]
	row.PinnedAt = mark.PinnedAt
	row.Snooze = snoozeRow(mark.Snooze)
	row.ReadState = nil
	if read, ok := view.reads[row.ID]; ok {
		row.ReadState = &read
	}
	if end, ok := view.turnEnds[row.ID]; ok {
		row.EndTurn = endTurnRow(&end)
	}
	if ask, ok := view.asks[row.ID]; ok {
		row.Ask = askRow(&ask)
	}
	if notice, ok := view.notices[row.ID]; ok {
		row.TurnCompletion = turnCompletionRow(notice)
	}
	var judgment *registry.TurnJudgment
	if current, ok := view.judgments[row.ID]; ok {
		judgment = &current
	}
	row.TurnState = turnStateOf(row, judgment, view.leads)
	row.HeldWork = view.held[row.ID]
}

// catalogViewArchive reports whether the view archives the row, and whether
// it archives the row's project.
func catalogViewArchive(row *dashboardSession, view catalogViewState) (bool, bool) {
	_, sessionArchived := view.archive.Sessions[row.ID]
	projectArchived := false
	if row.Project != nil {
		_, projectArchived = view.archive.Projects[row.Project.LocalID]
	}
	return sessionArchived || projectArchived, projectArchived
}

// dashboardProject is one project across every host: each checkout is one
// host's project with the same key, or the host's only checkout of a project
// without one. It is archived when every checkout is. Coordinators lists the
// project's coordinator on each host that has one; more than one is a
// conflict.
type dashboardProject struct {
	ID           string                             `json:"id"`
	DisplayName  string                             `json:"displayName"`
	Kind         string                             `json:"kind"`
	Key          string                             `json:"key,omitempty"`
	Repository   *project.Repository                `json:"repository,omitempty"`
	Checkouts    []dashboardProjectCheckout         `json:"checkouts"`
	Coordinators []dashboardProjectCoordinator      `json:"coordinators"`
	LastActive   time.Time                          `json:"lastActive,omitempty"`
	SessionCount int                                `json:"sessionCount"`
	Archived     bool                               `json:"archived"`
	Linked       []projectresolve.CatalogProjectRef `json:"linked,omitempty"`
	// LinkHeld is why this host keeps the project apart although a link joins it into another.
	LinkHeld    string                             `json:"linkHeld,omitempty"`
	SameHistory []projectresolve.CatalogProjectRef `json:"sameHistory,omitempty"`
	// MCP is "off" when terminal sessions of the project get no Agenthail tools.
	MCP string `json:"mcp"`
}

// dashboardProjectCoordinator is one host's coordinator of a project.
// SessionID is empty until its first message starts it.
type dashboardProjectCoordinator struct {
	Host      dashboardHost             `json:"host"`
	Name      string                    `json:"name"`
	Surface   string                    `json:"surface"`
	SessionID string                    `json:"sessionId,omitempty"`
	ChangedBy string                    `json:"changedBy,omitempty"`
	ChangedAt time.Time                 `json:"changedAt"`
	Replaced  *dashboardLeadReplacement `json:"replaced,omitempty"`
}

type dashboardProjectCheckout struct {
	Host         dashboardHost                     `json:"host"`
	ProjectID    string                            `json:"projectId"`
	RootPath     string                            `json:"rootPath"`
	Added        bool                              `json:"added"`
	SessionCount int                               `json:"sessionCount"`
	Archived     bool                              `json:"archived"`
	LinkedFrom   *projectresolve.CatalogProjectRef `json:"linkedFrom,omitempty"`
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
	return ScopeMatches(scope, SessionWorking(row.Status, row.Subagents) && !presentationNeedsYou(&row), row.Current || !row.PinnedAt.IsZero(), row.Archived)
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
// this host shows. A coordinator whose folder this host now lists under
// another project, after its repository's origin changed, belongs to no
// listed project; 'coordinator list' marks it.
func catalogProjects(rows []dashboardSession, records []registry.ProjectRecord, coordinators []registry.CoordinatorSpec, links projectresolve.ProjectLinkSet, archived map[string]time.Time, local dashboardHost, hostRef func(string) dashboardHost) []dashboardProject {
	byID := map[string]*dashboardProject{}
	order := []string{}
	named := map[string]bool{}
	roots := map[string][]string{}
	entry := func(found *projectresolve.CatalogProject) *dashboardProject {
		found = links.Apply(found)
		project, ok := byID[found.ID]
		if !ok {
			project = &dashboardProject{ID: found.ID, DisplayName: found.DisplayName, Kind: found.Kind, Key: found.Key, LinkHeld: links.Held[found.ID]}
			byID[found.ID] = project
			order = append(order, found.ID)
		}
		if found.LinkedFrom == nil && len(found.RootCommits) > 0 && roots[found.ID] == nil {
			roots[found.ID] = found.RootCommits
		}
		if found.LinkedFrom == nil && !named[found.ID] {
			named[found.ID] = true
			project.DisplayName, project.Kind, project.Key, project.Repository = found.DisplayName, found.Kind, found.Key, found.Repository
		}
		if found.LinkedFrom != nil && !slices.ContainsFunc(project.Linked, func(ref projectresolve.CatalogProjectRef) bool { return ref.ID == found.LinkedFrom.ID }) {
			project.Linked = append(project.Linked, *found.LinkedFrom)
		}
		return project
	}
	checkout := func(project *dashboardProject, host dashboardHost, found *projectresolve.CatalogProject) *dashboardProjectCheckout {
		for index := range project.Checkouts {
			if project.Checkouts[index].Host.ID == host.ID && project.Checkouts[index].ProjectID == found.LocalID {
				return &project.Checkouts[index]
			}
		}
		found = links.Apply(found)
		project.Checkouts = append(project.Checkouts, dashboardProjectCheckout{Host: host, ProjectID: found.LocalID, RootPath: found.RootPath, Added: found.Added, Archived: found.Archived, LinkedFrom: found.LinkedFrom})
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
		found := projectresolve.AddedCatalogProject(record, false)
		found.Archived = !record.ArchivedAt.IsZero()
		project := entry(found)
		checkout(project, local, found)
	}
	localRoots := map[string]bool{}
	for _, project := range byID {
		for _, at := range project.Checkouts {
			if at.Host.ID == local.ID {
				localRoots[at.RootPath] = true
			}
		}
	}
	for _, spec := range coordinators {
		if spec.HostID != "" {
			continue
		}
		if _, listed := byID[links.IdOf(spec.ProjectID)]; listed || localRoots[spec.ProjectRoot] {
			continue
		}
		found := coordinatorCatalogProject(spec)
		_, found.Archived = archived[found.LocalID]
		checkout(entry(found), local, found)
	}
	for _, spec := range coordinators {
		project, listed := byID[links.IdOf(spec.ProjectID)]
		if !listed {
			continue
		}
		host := local
		if spec.HostID != "" {
			host = hostRef(spec.HostID)
		}
		project.Coordinators = append(project.Coordinators, dashboardProjectCoordinator{Host: host, Name: spec.Name, Surface: spec.Surface, SessionID: spec.SessionID, ChangedBy: spec.ChangedBy, ChangedAt: spec.ChangedAt})
	}
	suggestSameHistory(byID, order, roots)
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
	sort.SliceStable(projects, func(i, j int) bool {
		return projectNameBefore(projects[i].DisplayName, projects[i].ID, projects[j].DisplayName, projects[j].ID)
	})
	return projects
}

// coordinatorCatalogProject is the project of this host's coordinator spec
// when no session or added project names it. Added projects are always
// listed, so it is a repository's project, rooted at its main checkout.
func coordinatorCatalogProject(spec registry.CoordinatorSpec) *projectresolve.CatalogProject {
	found := &projectresolve.CatalogProject{ID: spec.ProjectID, LocalID: projectresolve.CatalogImplicitProjectID(spec.ProjectRoot), DisplayName: projectresolve.CatalogProjectName(spec.ProjectRoot), RootPath: spec.ProjectRoot, Kind: projectresolve.CatalogProjectGit}
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

// requestObservation runs the observer pass now instead of at its next tick,
// so a session that just went idle receives its queued messages and its
// completion is published without waiting out the poll interval.
func (d *Daemon) requestObservation() {
	select {
	case d.observerNudge <- struct{}{}:
	default:
	}
}

func (d *Daemon) requestCatalogDiscovery() {
	select {
	case d.catalogNudge <- struct{}{}:
	default:
	}
}

func (d *Daemon) doProjectAction(ctx context.Context, _ principal, request projectRequest) (actionResult, error) {
	switch request.Action {
	case "project-add":
		if request.Join != "" {
			return d.doProjectJoin(ctx, request)
		}
		project, created, err := d.addProject(ctx, request.Path, request.Title)
		var missing *folderNotFoundError
		var handOver registry.ProjectHandOverError
		switch {
		case errors.As(err, &missing):
			return actionResult{}, actionFailed(http.StatusBadRequest, "folder_not_found", err.Error())
		case errors.As(err, &handOver):
			return actionResult{}, actionFailed(http.StatusConflict, "project_state_conflict", err.Error())
		case err != nil:
			return actionResult{}, invalidRequest(err.Error())
		default:
			return actionOK(map[string]any{"ok": true, "project": project, "created": created}), nil
		}
	case "project-share":
		return d.doProjectShare(ctx, request)
	case "project-join":
		return d.doProjectJoinFolder(ctx, request)
	case "project-unjoin":
		return d.doProjectUnjoin(request)
	case "project-link":
		return d.doProjectLink(request)
	case "project-unlink":
		return d.doProjectUnlink(request)
	case "project-setting":
		return d.doProjectSetting(request)
	case "project-remove", "project-archive", "project-unarchive", "project-rename":
	default:
		return actionResult{}, invalidRequest("unknown project action")
	}
	if request.ProjectID == "" {
		return actionResult{}, invalidRequest("projectId is required")
	}
	title := strings.TrimSpace(request.Title)
	if request.Action == "project-rename" && (title == "" || len(title) > projectTitleLimit) {
		return actionResult{}, invalidRequest(fmt.Sprintf("a project name is 1 to %d characters", projectTitleLimit))
	}
	if request.Action == "project-remove" {
		d.projectWrites.Lock()
		defer d.projectWrites.Unlock()
	}
	targets, err := d.localProjectCheckouts(request.ProjectID)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if request.Action == "project-remove" {
		return d.removeProject(ctx, request.ProjectID, targets)
	}
	var checkouts []map[string]any
	failed, changed := false, false
	for _, target := range targets {
		result := map[string]any{"host": d.localHost().Name, "projectId": target.ID, "rootPath": target.WorkspaceRoot, "ok": true}
		if request.Action == "project-rename" {
			if !target.Added {
				continue
			}
			if err := d.Registry.SetProjectTitle(target.ID, title); err != nil {
				result["ok"], result["error"] = false, err.Error()
				failed = true
			}
		} else if err := d.archiveLocalProject(target, request.Action == "project-archive"); err != nil {
			result["ok"], result["error"] = false, err.Error()
			failed = true
		}
		changed = changed || result["ok"] == true
		checkouts = append(checkouts, result)
	}
	if len(checkouts) > 0 {
		d.publishCatalogPresence()
		// A catalog pass that read the project state before this change may
		// still publish its rows; the pass this requests runs after it and
		// settles them.
		d.requestCatalogDiscovery()
	}
	// A paired host forwarding this action changes only its own checkouts,
	// so the action never travels back.
	listed := len(targets) > 0
	if _, fromHost := requestingHost(ctx); !fromHost {
		hostIDs, err := d.Registry.CatalogProjectHosts(request.ProjectID)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		for _, hostID := range hostIDs {
			listed = true
			result := map[string]any{"host": hostID, "ok": true}
			host, found := d.pairedHost(hostID)
			if !found {
				result["ok"], result["error"] = false, "host is no longer paired"
				failed = true
				checkouts = append(checkouts, result)
				continue
			}
			result["host"] = host.record.Name
			body := map[string]string{"action": request.Action, "projectId": request.ProjectID}
			if request.Action == "project-rename" {
				body["title"] = title
			}
			status, code, err := d.forwardProjectAction(ctx, host, body)
			switch {
			case err != nil:
				result["ok"], result["error"] = false, err.Error()
				failed = true
			case request.Action == "project-rename" && status == http.StatusConflict && code == "project_not_added":
				continue
			default:
				changed = true
			}
			checkouts = append(checkouts, result)
		}
	}
	switch {
	case !listed:
		return actionResult{}, actionFailed(http.StatusNotFound, "project_not_found", d.projectNotListed(request.ProjectID))
	case !changed && !failed:
		return actionResult{}, actionFailed(http.StatusConflict, "project_not_added", "project "+request.ProjectID+" was not added on any host, so it has no name of its own; add its folder as a project to name it")
	}
	status := http.StatusOK
	if failed {
		status = http.StatusBadGateway
	}
	answer := map[string]any{"ok": !failed, "projectId": request.ProjectID, "checkouts": checkouts}
	if request.Action == "project-rename" {
		answer["title"] = title
	} else {
		answer["archived"] = request.Action == "project-archive"
	}
	return actionResult{Status: status, Body: answer}, nil
}

const projectTitleLimit = 120

type projectRequest struct {
	Action      string
	ProjectID   string
	Path        string
	Title       string
	Survivor    string
	Setting     string
	Value       string
	Host        string
	Join        string
	JoinKey     string
	LedgerOwner string
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
		var found projectresolve.CatalogProject
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
		if found.ID != projectID && found.LocalID != projectID || seen[found.LocalID] || projectRootHeld(records, targets, spec) {
			continue
		}
		seen[found.LocalID] = true
		targets = append(targets, registry.ProjectRecord{ID: found.LocalID, WorkspaceRoot: found.RootPath, Title: found.DisplayName})
	}
	return targets, nil
}

// projectRootHeld reports whether a coordinator's project is already a project
// of this host: an added project with its id, key or folder, or a listed
// project at its folder. Then the coordinator names that project, or one its
// folder no longer resolves to, and stands for no project of its own.
func projectRootHeld(records, listed []registry.ProjectRecord, spec registry.CoordinatorSpec) bool {
	for _, record := range records {
		if record.Added && (record.ID == spec.ProjectID || record.Key == spec.ProjectID || record.WorkspaceRoot == spec.ProjectRoot) {
			return true
		}
	}
	for _, record := range listed {
		if record.WorkspaceRoot == spec.ProjectRoot {
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

// projectNotListed refuses an action on a project id this host does not
// list. A project's id follows its repository, so a client holding an id from
// before an origin change is told to refresh rather than reaching whatever
// project is in that folder now.
func (d *Daemon) projectNotListed(projectID string) string {
	return fmt.Sprintf("project %s is not listed on %s; it may be listed under another id now, for example after its repository's origin changed; refresh the project list and try again", projectID, d.hostIdentity().Name)
}

// removeProject removes one added project of this host. Its sessions move to
// the next project that contains them on the following catalog pass.
func (d *Daemon) removeProject(ctx context.Context, projectID string, targets []registry.ProjectRecord) (actionResult, error) {
	var added []registry.ProjectRecord
	for _, target := range targets {
		if target.Added {
			added = append(added, target)
		}
	}
	switch len(added) {
	case 0:
		if len(targets) > 0 {
			return actionResult{}, actionFailed(http.StatusConflict, "project_not_added", "project "+projectID+" was not added, so it cannot be removed; archive it to hide it")
		}
		return actionResult{}, actionFailed(http.StatusNotFound, "project_not_found", d.projectNotListed(projectID))
	case 1:
	default:
		roots := make([]string, 0, len(added))
		for _, target := range added {
			roots = append(roots, target.ID+" ("+target.WorkspaceRoot+")")
		}
		return actionResult{}, actionFailed(http.StatusConflict, "project_ambiguous", "project "+projectID+" names several added folders on this host: "+strings.Join(roots, ", ")+"; remove one by its id")
	}
	fallback := ""
	records, err := d.Registry.ListProjects()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	// A worktree project, or a folder inside one, hands nothing to the
	// repository's project, which keeps a lead and ledger of its own; a
	// joined checkout leaves its lead and ledger to the project's other
	// checkouts.
	if !added[0].Worktree && !added[0].Joined && !insideWorktreeProject(records, added[0].WorkspaceRoot) {
		repository, notRepository, err := projectresolve.ResolveCatalogRepository(ctx, added[0].WorkspaceRoot, nil, projectAddGitTimeout)
		switch {
		case err == nil:
			fallback = implicitProjectID(repository, added[0].WorkspaceRoot)
		case notRepository || folderGone(added[0].WorkspaceRoot):
		default:
			return actionResult{}, invalidRequest("read repository for " + added[0].WorkspaceRoot + ": " + err.Error() + "; the project is still added")
		}
	}
	outcome, err := d.Registry.RemoveProject(added[0].ID, fallback)
	var handOver registry.ProjectHandOverError
	switch {
	case errors.As(err, &handOver):
		return actionResult{}, actionFailed(http.StatusConflict, "project_state_conflict", err.Error())
	case err != nil:
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	d.projectMovesApplied([]registry.ProjectMoveOutcome{outcome})
	d.requestCatalogDiscovery()
	return actionOK(map[string]any{"ok": true, "projectId": added[0].ID, "rootPath": added[0].WorkspaceRoot}), nil
}

func folderGone(path string) bool {
	_, err := os.Stat(path)
	return errors.Is(err, os.ErrNotExist)
}

// forwardProjectAction runs a project action on a paired host. A refusal is
// returned as its status and error code; only a host that cannot answer is
// an error.
func (d *Daemon) forwardProjectAction(ctx context.Context, host *pairedHost, request map[string]string) (int, string, error) {
	answer, err := d.callProjectAction(ctx, host, request)
	if err != nil {
		return 0, "", err
	}
	if answer.status == http.StatusOK {
		return answer.status, "", nil
	}
	var envelope struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(answer.payload, &envelope)
	if answer.status == http.StatusConflict && envelope.Error.Code == "project_not_added" {
		return answer.status, envelope.Error.Code, nil
	}
	return answer.status, envelope.Error.Code, fmt.Errorf("%s answered %d: %s", host.record.Name, answer.status, strings.TrimSpace(string(answer.payload)))
}

// callProjectAction runs a project action on a paired host and returns its
// answer, a refusal too; only a host that cannot answer is an error.
func (d *Daemon) callProjectAction(ctx context.Context, host *pairedHost, request map[string]string) (hostAnswer, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return hostAnswer{}, err
	}
	key := actionKey(ctx)
	if key == "" {
		key = "fwd-" + uuid.NewString()
	}
	header := http.Header{actionIdempotencyHeader: []string{key + ":" + request["action"] + ":" + host.record.ID}}
	response, err := host.client.Do(ctx, http.MethodPost, "/api/v1/actions", nil, body, header)
	if err != nil {
		return hostAnswer{}, err
	}
	return readHostAnswer(host, response)
}

func (d *Daemon) archiveSession(sessionID string, at time.Time) error {
	if err := d.Registry.ArchiveSession(sessionID, at); err != nil {
		return err
	}
	go d.closeBrowserTabs(sessionID)
	d.releaseSimulators(sessionID)
	return nil
}

func (d *Daemon) doSessionArchive(ctx context.Context, action string, session *surface.Session) (actionResult, error) {
	sessionID := session.ID
	var run surface.RunStop
	var err error
	if action == "session-archive" {
		task, found, taskErr := d.Registry.TaskForChild(sessionID)
		if taskErr != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", taskErr.Error())
		}
		if run, err = d.stopRun(ctx, session); err != nil {
			return actionResult{}, actionFailed(http.StatusBadGateway, "stop_failed", fmt.Sprintf("%s was not archived because its run could not be stopped: %s", sessionID, err))
		}
		err = d.archiveSession(sessionID, time.Now().UTC())
		if err == nil && found && task.State == registry.TaskRunning {
			d.publishTaskSettled(sessionID, registry.TaskCancelled, "")
		}
		if err == nil {
			d.settleWork(sessionID, registry.WorkSettledArchived)
		}
	} else {
		err = d.Registry.UnarchiveSession(sessionID)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return actionResult{}, actionFailed(http.StatusNotFound, "not_found", "session not found")
	}
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error())
	}
	d.publishCatalogPresence()
	// A catalog pass that read the archive state before this change may still
	// publish its rows; the pass this requests runs after it and settles them.
	d.requestCatalogDiscovery()
	answer := map[string]any{"ok": true, "sessionId": sessionID, "archived": action == "session-archive"}
	if action == "session-archive" {
		answer["run"] = run
	}
	return actionOK(answer), nil
}

// stopRun runs before the archive is written: a session still working is
// unarchived by the next catalog pass, so an archive that left it running
// would not hold.
func (d *Daemon) stopRun(ctx context.Context, session *surface.Session) (surface.RunStop, error) {
	adapter, err := d.adapters().For(session)
	if err != nil {
		return surface.RunStop{}, nil
	}
	stopper, ok := adapter.(surface.RunStopper)
	if !ok {
		return surface.RunStop{}, nil
	}
	ctx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	run, err := stopper.StopRun(ctx, session)
	if err == nil && run.Stopped {
		_ = d.Registry.RecordHistory(registry.HistoryEntry{Kind: "session-operation", SessionID: session.ID, Result: "session-archive: stopped its run"})
	}
	return run, err
}

// addProject adds a folder as a project of this host. A folder in a linked
// worktree is added at the same place in the main checkout, so the project
// holds that folder in every worktree. A folder already added answers with
// its project, unchanged, and created false.
// The person waits on an add, so it may take longer than a discovery pass.
var projectAddGitTimeout = 30 * time.Second

func (d *Daemon) addProject(ctx context.Context, path, title string) (projectresolve.CatalogProject, bool, error) {
	d.projectWrites.Lock()
	defer d.projectWrites.Unlock()
	plan, err := d.planProjectAdd(ctx, path, title)
	if err != nil {
		return projectresolve.CatalogProject{}, false, err
	}
	if existing := plan.existing(); existing != nil {
		return *projectresolve.AddedCatalogProject(*existing, plan.inRepository), false, nil
	}
	return d.recordProjectAdd(plan)
}

// projectAddPlan is the record adding a folder would write, and the
// implicit project whose lead and archive state it takes over.
type projectAddPlan struct {
	record       registry.ProjectRecord
	supersedes   string
	inRepository bool
	records      []registry.ProjectRecord
}

// existing is the project already added at the planned folder.
func (p projectAddPlan) existing() *registry.ProjectRecord {
	for index := range p.records {
		if p.records[index].Added && p.records[index].WorkspaceRoot == p.record.WorkspaceRoot {
			return &p.records[index]
		}
	}
	return nil
}

func (d *Daemon) planProjectAdd(ctx context.Context, path, title string) (projectAddPlan, error) {
	path, err := workspace.NormalizeCWD(path)
	if err != nil || path == "" {
		return projectAddPlan{}, fmt.Errorf("a folder path is required")
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		return projectAddPlan{}, &folderNotFoundError{path: path, host: d.hostIdentity().Name}
	}
	if path, err = projectresolve.CanonicalCatalogPath(path); err != nil {
		return projectAddPlan{}, err
	}
	plan := projectAddPlan{record: registry.ProjectRecord{ID: "p-" + uuid.NewString(), WorkspaceRoot: path, Title: strings.TrimSpace(title), CreatedAt: time.Now().UTC()}}
	if len(plan.record.Title) > projectTitleLimit {
		return projectAddPlan{}, fmt.Errorf("a project name is 1 to %d characters", projectTitleLimit)
	}
	repository, notRepository, err := projectresolve.ResolveCatalogRepository(ctx, path, nil, projectAddGitTimeout)
	plan.inRepository = err == nil
	if !plan.inRepository && !notRepository {
		return projectAddPlan{}, fmt.Errorf("read repository for %s: %w", path, err)
	}
	if plan.records, err = d.Registry.ListProjects(); err != nil {
		return projectAddPlan{}, err
	}
	// A worktree project, or a folder inside one, splits off part of its
	// repository's project and takes over nothing.
	switch {
	case plan.inRepository && repository.LinkedWorktreeRoot(path):
		plan.record.Worktree = true
	case plan.inRepository && !insideWorktreeProject(plan.records, path):
		plan.record.WorkspaceRoot = repository.Translate(path)
		if plan.record.Key = repository.Key(plan.record.WorkspaceRoot); plan.record.Key != "" {
			plan.record.Repository, _ = json.Marshal(repository.Repository)
		}
		plan.supersedes = implicitProjectID(repository, plan.record.WorkspaceRoot)
	}
	if plan.record.Title == "" {
		plan.record.Title = filepath.Base(plan.record.WorkspaceRoot)
	}
	if id, kept, err := d.keptProjectID(plan.records, plan.record.WorkspaceRoot); err != nil {
		return projectAddPlan{}, err
	} else if kept {
		plan.record.ID = id
	}
	return plan, nil
}

func (d *Daemon) recordProjectAdd(plan projectAddPlan) (projectresolve.CatalogProject, bool, error) {
	outcome, err := d.Registry.AddProject(plan.record, plan.supersedes)
	var taken *registry.WorkspaceTakenError
	if errors.As(err, &taken) {
		records, listErr := d.Registry.ListProjects()
		if listErr != nil {
			return projectresolve.CatalogProject{}, false, listErr
		}
		for _, existing := range records {
			if existing.ID == taken.ProjectID {
				return *projectresolve.AddedCatalogProject(existing, plan.inRepository), false, nil
			}
		}
		return projectresolve.CatalogProject{}, false, taken
	}
	if err != nil {
		return projectresolve.CatalogProject{}, false, err
	}
	d.projectMovesApplied([]registry.ProjectMoveOutcome{outcome})
	d.requestCatalogDiscovery()
	return *projectresolve.AddedCatalogProject(plan.record, plan.inRepository), true, nil
}

func insideWorktreeProject(records []registry.ProjectRecord, path string) bool {
	return projectresolve.WorktreeProjectHolding(records, path) != nil
}

// keptProjectID is the id of a project removed from root that still keeps
// something under it: its ledger, or this host's lead set for that folder.
// Adding the folder again takes that id back, so the project returns with
// both.
func (d *Daemon) keptProjectID(records []registry.ProjectRecord, root string) (string, bool, error) {
	if id, kept, err := d.Registry.RemovedProjectLedger(root); err != nil || kept {
		return id, kept, err
	}
	coordinators, err := d.Registry.LocalCoordinators()
	if err != nil {
		return "", false, err
	}
	for _, spec := range coordinators {
		if spec.ProjectRoot == root && strings.HasPrefix(spec.ProjectID, "p-") && !slices.ContainsFunc(records, func(record registry.ProjectRecord) bool { return record.ID == spec.ProjectID }) {
			return spec.ProjectID, true, nil
		}
	}
	return "", false, nil
}

// implicitProjectID is the project a repository's main checkout has when no
// added project claims it.
func implicitProjectID(repository projectresolve.CatalogRepository, root string) string {
	if repository.MainRoot != root {
		return ""
	}
	if key := repository.Key(root); key != "" {
		return key
	}
	return projectresolve.CatalogImplicitProjectID(root)
}

// folderNotFoundError refuses to add a folder its host does not have.
type folderNotFoundError struct {
	path string
	host string
}

func (e *folderNotFoundError) Error() string {
	return fmt.Sprintf("%s is not a folder on %s", e.path, e.host)
}

// refreshProjectRepositories keeps each added project's key current with its
// repository's remote.
func (d *Daemon) refreshProjectRepositories(ctx context.Context, records []registry.ProjectRecord, now time.Time) {
	for index := range records {
		record := &records[index]
		if !record.Added || record.Worktree || record.Joined || insideWorktreeProject(records, record.WorkspaceRoot) {
			continue
		}
		key, repository, ok := d.catalogIdentities.RootRepository(ctx, record.WorkspaceRoot, now)
		if ok && key != "" {
			d.observeRepositoryKey(record.WorkspaceRoot, key)
		}
		if !ok || (key == record.Key && bytes.Equal(repository, record.Repository)) {
			continue
		}
		if err := d.Registry.SetProjectRepository(record.ID, key, repository); errors.Is(err, sql.ErrNoRows) {
			continue
		} else if err != nil {
			d.log.Printf("project %s repository: %s", record.WorkspaceRoot, err)
			continue
		}
		record.Key, record.Repository = key, repository
	}
}

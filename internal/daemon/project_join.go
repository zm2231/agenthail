package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

type projectShare struct {
	Key         string `json:"key"`
	Title       string `json:"title"`
	LedgerOwner string `json:"ledgerOwner"`
}

// doProjectJoin joins a folder on any host to a project. The project's host
// gives the project a shared key if it has none (project-share), then the
// folder's host records the folder under that key (project-join).
func (d *Daemon) doProjectJoin(ctx context.Context, request projectRequest) (actionResult, error) {
	if strings.TrimSpace(request.Path) == "" {
		return actionResult{}, invalidRequest("a folder path is required")
	}
	share, err := d.shareProject(ctx, strings.TrimSpace(request.Join))
	if err != nil {
		return actionResult{}, err
	}
	hostRef := strings.TrimSpace(request.Host)
	if hostRef == "" || d.isLocalHostRef(hostRef) {
		return d.joinFolder(ctx, request.Path, share)
	}
	host, found := d.pairedHost(hostRef)
	if !found {
		return actionResult{}, actionFailed(http.StatusNotFound, "host_not_found", fmt.Sprintf("no paired host is named %q; see agenthail hosts", hostRef))
	}
	answer, err := d.callProjectAction(ctx, host, map[string]string{"action": "project-join", "path": request.Path, "joinKey": share.Key, "title": share.Title, "ledgerOwner": share.LedgerOwner})
	if err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "host_unreachable", fmt.Sprintf("%s could not be reached, so %s was not joined to %s: %s", host.record.Name, request.Path, share.Title, err))
	}
	if answer.status != http.StatusOK {
		return actionResult{}, hostRefusal(host, answer)
	}
	var body map[string]any
	if err := json.Unmarshal(answer.payload, &body); err != nil {
		return actionResult{}, actionFailed(http.StatusBadGateway, "host_answer", fmt.Sprintf("%s answered the join with %s", host.record.Name, err))
	}
	return actionOK(body), nil
}

func hostRefusal(host *pairedHost, answer hostAnswer) error {
	var envelope struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(answer.payload, &envelope) != nil || envelope.Error.Code == "" {
		return actionFailed(http.StatusBadGateway, "host_unreachable", fmt.Sprintf("%s could not be reached (it answered %d), so nothing was joined", host.record.Name, answer.status))
	}
	if envelope.Error.Code == "invalid_request" && strings.Contains(envelope.Error.Message, "unknown project action") {
		return actionFailed(http.StatusConflict, "host_incompatible", fmt.Sprintf("Host %s needs an update to join folders to projects; nothing was joined", host.record.Name))
	}
	return actionFailed(answer.status, envelope.Error.Code, host.record.Name+": "+envelope.Error.Message)
}

// shareProject finds the project a join names and the key its folders share.
// A project of a paired host, or a key this host holds no checkout of, is
// asked of a host that has it.
func (d *Daemon) shareProject(ctx context.Context, projectID string) (projectShare, error) {
	if projectID == "" {
		return projectShare{}, invalidRequest("join names the project to join")
	}
	if hostRef, target, qualified := hosts.Split(projectID); qualified {
		if d.isLocalHostRef(hostRef) {
			return d.shareLocalProject(target)
		}
		host, found := d.pairedHost(hostRef)
		if !found {
			return projectShare{}, actionFailed(http.StatusNotFound, "host_not_found", fmt.Sprintf("no paired host is named %q; see agenthail hosts", hostRef))
		}
		return d.shareProjectOn(ctx, host, target)
	}
	share, err := d.shareLocalProject(projectID)
	var failure *actionError
	if !errors.As(err, &failure) || failure.Code != "project_not_found" || !hosts.ProjectKey(projectID) {
		return share, err
	}
	hostIDs, listErr := d.Registry.CatalogProjectHosts(projectID)
	if listErr != nil {
		return projectShare{}, actionFailed(http.StatusInternalServerError, "registry_error", listErr.Error())
	}
	for _, hostID := range hostIDs {
		if host, found := d.pairedHost(hostID); found {
			return d.shareProjectOn(ctx, host, projectID)
		}
	}
	return projectShare{}, err
}

func (d *Daemon) shareProjectOn(ctx context.Context, host *pairedHost, projectID string) (projectShare, error) {
	answer, err := d.callProjectAction(ctx, host, map[string]string{"action": "project-share", "projectId": projectID})
	if err != nil {
		return projectShare{}, actionFailed(http.StatusBadGateway, "host_unreachable", fmt.Sprintf("%s, which holds the project, could not be reached, so nothing was joined: %s", host.record.Name, err))
	}
	if answer.status != http.StatusOK {
		return projectShare{}, hostRefusal(host, answer)
	}
	var share projectShare
	if err := json.Unmarshal(answer.payload, &share); err != nil || !hosts.ProjectKey(share.Key) {
		return projectShare{}, actionFailed(http.StatusBadGateway, "host_answer", fmt.Sprintf("%s did not name the project's key", host.record.Name))
	}
	return share, nil
}

func (d *Daemon) doProjectShare(ctx context.Context, request projectRequest) (actionResult, error) {
	if _, fromHost := requestingHost(ctx); !fromHost {
		return actionResult{}, invalidRequest("project-share is asked by a paired host; join a folder with project-add and join")
	}
	share, err := d.shareLocalProject(request.ProjectID)
	if err != nil {
		return actionResult{}, err
	}
	return actionOK(share), nil
}

// shareLocalProject gives an added project of this host the shared key, if
// it has no key yet, and names the host keeping its ledger.
func (d *Daemon) shareLocalProject(projectID string) (projectShare, error) {
	records, err := d.Registry.ListProjects()
	if err != nil {
		return projectShare{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	var record *registry.ProjectRecord
	for index := range records {
		if records[index].Added && (records[index].ID == projectID || records[index].Key == projectID) {
			record = &records[index]
			break
		}
	}
	if record == nil {
		return projectShare{}, actionFailed(http.StatusNotFound, "project_not_found", fmt.Sprintf("%s is not an added project on %s; add its folder first with agenthail project add", projectID, d.hostIdentity().Name))
	}
	if record.Key == "" {
		key := hosts.SharedPrefix + uuid.NewString()
		var refusal registry.ProjectJoinRefusal
		switch err := d.Registry.JoinProjectKey(record.ID, key); {
		case errors.As(err, &refusal):
			return projectShare{}, actionFailed(http.StatusConflict, "project_history", fmt.Sprintf("%s cannot be shared with another computer: %s", record.Title, refusal.Reason))
		case err != nil:
			return projectShare{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		record.Key, record.Joined = key, true
		d.requestCatalogDiscovery()
	}
	ledgerKey, err := projectresolve.LedgerKey(d.Registry, projectresolve.AddedCatalogProject(*record, false))
	if err != nil {
		return projectShare{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	share := projectShare{Key: record.Key, Title: record.Title, LedgerOwner: d.hostIdentity().ID}
	if ledgerKey == record.Key {
		owner, _, err := d.ledgerStore().Owner(ledgerKey)
		if err != nil {
			return projectShare{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		if owner != "" {
			share.LedgerOwner = owner
		}
	}
	return share, nil
}

func (d *Daemon) doProjectJoinFolder(ctx context.Context, request projectRequest) (actionResult, error) {
	if _, fromHost := requestingHost(ctx); !fromHost {
		return actionResult{}, invalidRequest("project-join is sent by a paired host; join a folder with project-add and join")
	}
	if !hosts.ProjectKey(request.JoinKey) || strings.TrimSpace(request.Title) == "" {
		return actionResult{}, invalidRequest("project-join needs the project's key and name")
	}
	return d.joinFolder(ctx, request.Path, projectShare{Key: request.JoinKey, Title: strings.TrimSpace(request.Title), LedgerOwner: request.LedgerOwner})
}

// joinFolder records a folder of this host as a checkout of the shared
// project. The folder's own project may give nothing up: a folder of a
// repository with a remote is already one project on every host, and one
// with its own lead or ledger would need two histories merged.
func (d *Daemon) joinFolder(ctx context.Context, path string, share projectShare) (actionResult, error) {
	d.projectAdds.Lock()
	defer d.projectAdds.Unlock()
	plan, err := d.planProjectAdd(ctx, path, share.Title)
	var missing *folderNotFoundError
	switch {
	case errors.As(err, &missing):
		return actionResult{}, actionFailed(http.StatusBadRequest, "folder_not_found", err.Error())
	case err != nil:
		return actionResult{}, invalidRequest(err.Error())
	}
	folder := plan.record.WorkspaceRoot
	existing := plan.existing()
	switch {
	case existing != nil && existing.Key == share.Key:
		return actionOK(map[string]any{"ok": true, "project": projectresolve.AddedCatalogProject(*existing, plan.inRepository), "created": false}), nil
	case existing != nil && existing.Joined:
		return actionResult{}, actionFailed(http.StatusConflict, "project_joined", fmt.Sprintf("%s is already joined to %s; unjoin it first with agenthail project unjoin", folder, existing.Title))
	case existing != nil && existing.Key != "", existing == nil && plan.record.Key != "":
		return actionResult{}, actionFailed(http.StatusConflict, "project_keyed", fmt.Sprintf("%s is in a repository with a remote, which is already one project on every computer; join two such projects with agenthail project link", folder))
	}
	own := []string{plan.record.ID, plan.supersedes}
	if existing != nil {
		own = []string{existing.ID}
	}
	for _, id := range own {
		if id == "" {
			continue
		}
		held, err := d.Registry.ProjectHistory(id)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		if len(held) > 0 {
			return actionResult{}, actionFailed(http.StatusConflict, "project_history", fmt.Sprintf("%s already has %s of its own, and two histories cannot be merged; join the other way round, adding %s to this folder's project", folder, strings.Join(held, " and "), share.Title))
		}
	}
	if err := d.replicateJoinedLedger(ctx, share); err != nil {
		return actionResult{}, err
	}
	if existing != nil {
		var refusal registry.ProjectJoinRefusal
		switch err := d.Registry.JoinProjectKey(existing.ID, share.Key); {
		case errors.As(err, &refusal):
			return actionResult{}, actionFailed(http.StatusConflict, "project_history", fmt.Sprintf("%s cannot join %s: %s", folder, share.Title, refusal.Reason))
		case err != nil:
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		if err := d.Registry.SetProjectTitle(existing.ID, share.Title); err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		joined := *existing
		joined.Key, joined.Joined, joined.Title, joined.Repository = share.Key, true, share.Title, nil
		d.requestCatalogDiscovery()
		return actionOK(map[string]any{"ok": true, "project": projectresolve.AddedCatalogProject(joined, plan.inRepository), "created": true}), nil
	}
	plan.record.Key, plan.record.Joined, plan.record.Title = share.Key, true, share.Title
	project, created, err := d.recordProjectAdd(plan)
	var handOver registry.ProjectHandOverError
	switch {
	case errors.As(err, &handOver):
		return actionResult{}, actionFailed(http.StatusConflict, "project_state_conflict", err.Error())
	case err != nil:
		return actionResult{}, invalidRequest(err.Error())
	case project.Key != share.Key:
		return actionResult{}, actionFailed(http.StatusConflict, "project_state_conflict", fmt.Sprintf("%s became a project of its own while it was being joined; join it again", folder))
	}
	return actionOK(map[string]any{"ok": true, "project": project, "created": created}), nil
}

// replicateJoinedLedger makes this host's ledger of the shared project a
// replica of the one its project's host keeps, so the project has one ledger.
func (d *Daemon) replicateJoinedLedger(ctx context.Context, share projectShare) error {
	if share.LedgerOwner == "" || share.LedgerOwner == d.hostIdentity().ID {
		return nil
	}
	owner, _, err := d.ledgerStore().Owner(share.Key)
	if err != nil {
		return actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if owner == share.LedgerOwner {
		return nil
	}
	host, found := d.pairedHost(share.LedgerOwner)
	if !found {
		return actionFailed(http.StatusConflict, "host_not_found", fmt.Sprintf("%s keeps the ledger of %s, but %s has not paired with it; pair them with agenthail hosts add, then join again", share.LedgerOwner, share.Title, d.hostIdentity().Name))
	}
	if _, err := d.assignLedgerOwner(ctx, host, share.Key); err != nil {
		var failure *actionError
		if errors.As(err, &failure) {
			return actionFailed(failure.Status, failure.Code, fmt.Sprintf("the ledger of %s could not be shared with %s, so nothing was joined: %s", share.Title, d.hostIdentity().Name, failure.Message))
		}
		return err
	}
	return nil
}

// doProjectUnjoin makes a joined checkout of this host a project of its own
// again. The project it leaves keeps its key, lead and ledger.
func (d *Daemon) doProjectUnjoin(request projectRequest) (actionResult, error) {
	if request.ProjectID == "" {
		return actionResult{}, invalidRequest("projectId is required")
	}
	records, err := d.Registry.ListProjects()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	var joined []registry.ProjectRecord
	known := false
	for _, record := range records {
		if !record.Added || record.ID != request.ProjectID && record.Key != request.ProjectID {
			continue
		}
		known = true
		if record.Joined {
			joined = append(joined, record)
		}
	}
	switch {
	case len(joined) == 1:
	case len(joined) > 1:
		roots := make([]string, 0, len(joined))
		for _, record := range joined {
			roots = append(roots, record.ID+" ("+record.WorkspaceRoot+")")
		}
		return actionResult{}, actionFailed(http.StatusConflict, "project_ambiguous", "project "+request.ProjectID+" is joined in several folders on this host: "+strings.Join(roots, ", ")+"; unjoin one by its id")
	case known:
		return actionResult{}, actionFailed(http.StatusConflict, "project_not_joined", "project "+request.ProjectID+" has no folder joined on "+d.hostIdentity().Name)
	default:
		return actionResult{}, actionFailed(http.StatusNotFound, "project_not_found", d.projectNotListed(request.ProjectID))
	}
	switch err := d.Registry.UnjoinProject(joined[0].ID); {
	case errors.Is(err, sql.ErrNoRows):
		return actionResult{}, actionFailed(http.StatusConflict, "project_not_joined", "project "+request.ProjectID+" has no folder joined on "+d.hostIdentity().Name)
	case err != nil:
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	d.requestCatalogDiscovery()
	return actionOK(map[string]any{"ok": true, "projectId": joined[0].ID, "rootPath": joined[0].WorkspaceRoot, "left": joined[0].Key}), nil
}

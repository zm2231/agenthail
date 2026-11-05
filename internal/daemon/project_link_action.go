package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) doProjectLink(request projectRequest) (actionResult, error) {
	joined, survivor := request.ProjectID, request.Survivor
	if joined == "" || survivor == "" {
		return actionResult{}, invalidRequest("projectId and survivor are required")
	}
	if joined == survivor {
		return actionResult{}, invalidRequest("a project cannot be linked into itself")
	}
	for _, id := range []string{joined, survivor} {
		if !registry.SharedProjectID(id) && strings.Contains(id, hosts.Separator) {
			return actionResult{}, invalidRequest("a folder of another host can only be linked on that host")
		}
	}
	links, records, err := d.projectLinkState()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	name := func(id string) string { return projectIDName(id, records) }
	if current, found := links[joined]; found && current.Linked {
		if current.Survivor == survivor {
			return projectLinkReply(current, false), nil
		}
		return actionResult{}, actionFailed(http.StatusConflict, "project_linked", fmt.Sprintf("%s is already linked into %s; unlink it first: agenthail project unlink %s", name(joined), name(current.Survivor), projectLinkSelector(joined)))
	}
	if current, found := links[survivor]; found && current.Linked {
		return actionResult{}, actionFailed(http.StatusConflict, "project_linked", fmt.Sprintf("%s is itself linked into %s; link into %s instead", name(survivor), name(current.Survivor), name(current.Survivor)))
	}
	for _, link := range links {
		if link.Linked && link.Survivor == joined {
			return actionResult{}, actionFailed(http.StatusConflict, "project_linked", fmt.Sprintf("%s has %s linked into it; link %s into %s instead", name(joined), name(link.Joined), name(survivor), name(joined)))
		}
	}
	joinedHistory, err := d.Registry.ProjectHistory(joined)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if len(joinedHistory) > 0 {
		survivorHistory, err := d.Registry.ProjectHistory(survivor)
		if err != nil {
			return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
		}
		if len(survivorHistory) > 0 {
			return actionResult{}, actionFailed(http.StatusConflict, "project_history", fmt.Sprintf("%s has %s and %s has %s; merging two projects' leads and ledger boards isn't supported", name(joined), strings.Join(joinedHistory, " and "), name(survivor), strings.Join(survivorHistory, " and ")))
		}
		return actionResult{}, actionFailed(http.StatusConflict, "project_history", fmt.Sprintf("%s already has %s of its own; link the other way round: agenthail project link %s %s", name(joined), strings.Join(joinedHistory, " and "), projectLinkSelector(survivor), projectLinkSelector(joined)))
	}
	link, err := d.Registry.SetProjectLink(joined, survivor, true)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	d.projectLinksChanged()
	return projectLinkReply(link, true), nil
}

func (d *Daemon) doProjectUnlink(request projectRequest) (actionResult, error) {
	if request.ProjectID == "" {
		return actionResult{}, invalidRequest("projectId is required")
	}
	links, records, err := d.projectLinkState()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	current, found := links[request.ProjectID]
	if !found || !current.Linked {
		return actionResult{}, actionFailed(http.StatusConflict, "project_not_linked", fmt.Sprintf("%s is not linked into another project", projectIDName(request.ProjectID, records)))
	}
	link, err := d.Registry.SetProjectLink(current.Joined, current.Survivor, false)
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	d.projectLinksChanged()
	return projectLinkReply(link, true), nil
}

func projectLinkReply(link registry.ProjectLink, changed bool) actionResult {
	return actionOK(map[string]any{"ok": true, "changed": changed, "link": map[string]any{"joined": link.Joined, "survivor": link.Survivor, "linked": link.Linked}})
}

func (d *Daemon) projectLinkState() (map[string]registry.ProjectLink, []registry.ProjectRecord, error) {
	links, err := d.Registry.ProjectLinks()
	if err != nil {
		return nil, nil, err
	}
	records, err := d.Registry.ListProjects()
	if err != nil {
		return nil, nil, err
	}
	byJoined := make(map[string]registry.ProjectLink, len(links))
	for _, link := range links {
		byJoined[link.Joined] = link
	}
	return byJoined, records, nil
}

func projectLinkSelector(id string) string {
	if folder, found := strings.CutPrefix(id, "implicit:"); found {
		return folder
	}
	return id
}

func (d *Daemon) projectLinksChanged() {
	d.flushCatalog("project links")
	if err := d.refreshProjectLinks(); err != nil {
		d.log.Printf("project links: %s", err)
	}
	d.publishCatalogPresence()
	d.requestCatalogDiscovery()
}

func (d *Daemon) sharedProjectLinks() ([]registry.ProjectLink, error) {
	links, err := d.Registry.ProjectLinks()
	if err != nil {
		return nil, err
	}
	shared := make([]registry.ProjectLink, 0, len(links))
	for _, link := range links {
		if link.Shared() {
			shared = append(shared, link)
		}
	}
	return shared, nil
}

func (d *Daemon) adoptProjectLinks(links []registry.ProjectLink) error {
	changed, err := d.Registry.AdoptProjectLinks(links)
	if err != nil || !changed {
		return err
	}
	d.projectLinksChanged()
	return nil
}

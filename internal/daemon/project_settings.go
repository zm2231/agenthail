package daemon

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) doProjectSetting(request projectRequest) (actionResult, error) {
	if request.ProjectID == "" {
		return actionResult{}, invalidRequest("projectId is required")
	}
	if !registry.SharedProjectID(request.ProjectID) && strings.Contains(request.ProjectID, hosts.Separator) {
		return actionResult{}, invalidRequest("a folder of another host can only be set on that host")
	}
	links, records, err := d.projectLinkState()
	if err != nil {
		return actionResult{}, actionFailed(http.StatusInternalServerError, "registry_error", err.Error())
	}
	if link, found := links[request.ProjectID]; found && link.Linked {
		return actionResult{}, actionFailed(http.StatusConflict, "project_linked", fmt.Sprintf("%s is linked into %s, whose settings it follows; set it there", projectIDName(link.Joined, records), projectIDName(link.Survivor, records)))
	}
	setting, err := d.Registry.SetProjectSetting(request.ProjectID, request.Setting, request.Value)
	if err != nil {
		return actionResult{}, invalidRequest(err.Error())
	}
	d.projectSettingsChanged()
	return actionOK(map[string]any{"ok": true, "setting": map[string]any{"project": setting.Project, "name": setting.Name, "value": setting.Value}}), nil
}

func (d *Daemon) projectToolsOff(project *CatalogProject) bool {
	if project == nil {
		return false
	}
	value, err := d.Registry.ProjectSettingValue(project.ID, registry.ProjectSettingMCP)
	if err != nil {
		d.log.Printf("MCP socket: reading the tools setting of %s failed: %s", project.DisplayName, err)
		return true
	}
	return value == registry.ProjectMCPOff
}

func (d *Daemon) projectSettingsChanged() {
	d.flushCatalog("project settings")
	d.publishCatalogPresence()
}

func (d *Daemon) adoptProjectSettings(settings []registry.ProjectSetting) error {
	changed, err := d.Registry.AdoptProjectSettings(settings)
	if err != nil || !changed {
		return err
	}
	d.projectSettingsChanged()
	return nil
}

func sharedProjectSettings(settings []registry.ProjectSetting) []registry.ProjectSetting {
	shared := make([]registry.ProjectSetting, 0, len(settings))
	for _, setting := range settings {
		if registry.SharedProjectID(setting.Project) {
			shared = append(shared, setting)
		}
	}
	return shared
}

func setProjectMCP(projects []dashboardProject, settings []registry.ProjectSetting) {
	values := map[string]string{}
	for _, setting := range settings {
		if setting.Name == registry.ProjectSettingMCP {
			values[setting.Project] = setting.Value
		}
	}
	for i := range projects {
		projects[i].MCP = registry.ProjectMCPOn
		if value, ok := values[projects[i].ID]; ok {
			projects[i].MCP = value
		}
	}
}

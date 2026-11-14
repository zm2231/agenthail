package daemon

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/testkit"
)

func projectTools(t *testing.T, node *federationNode, project string) string {
	t.Helper()
	value, err := node.reg.ProjectSettingValue(project, registry.ProjectSettingMCP)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestAProjectsToolsSettingReachesEveryPairedHostAndOneMadeOfflineWins(t *testing.T) {
	laptop, server := linkedFederation(t)
	linkAction(t, laptop, map[string]string{"action": "project-setting", "projectId": upstreamProject, "setting": registry.ProjectSettingMCP, "value": registry.ProjectMCPOff})
	for name, node := range map[string]*federationNode{"laptop": laptop, "server": server} {
		testkit.Eventually(t, name+" has the upstream's tools off", 10*time.Second, func() bool {
			return projectTools(t, node, upstreamProject) == registry.ProjectMCPOff
		})
	}
	const folder = "implicit:/home/me/notes"
	if _, err := server.reg.SetProjectSetting(folder, registry.ProjectSettingMCP, registry.ProjectMCPOff); err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Projects []dashboardProject        `json:"projects"`
		Settings []registry.ProjectSetting `json:"projectSettings"`
	}
	response := server.request(t, http.MethodGet, "/api/v1/snapshot?fresh=1", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &snapshot); err != nil {
		t.Fatalf("%v: %s", err, response.Body)
	}
	if len(snapshot.Settings) != 1 || snapshot.Settings[0].Project != upstreamProject {
		t.Fatalf("the snapshot paired hosts read carries %+v; want only the upstream's setting, never a folder's", snapshot.Settings)
	}
	rows := map[string]string{}
	for _, project := range snapshot.Projects {
		rows[project.ID] = project.MCP
	}
	if rows[upstreamProject] != registry.ProjectMCPOff {
		t.Fatalf("project rows carry %v; want the upstream's tools off", rows)
	}

	for _, node := range []*federationNode{laptop, server} {
		node.gate.proxyDown.Store(true)
		node.server.CloseClientConnections()
	}
	linkAction(t, server, map[string]string{"action": "project-setting", "projectId": upstreamProject, "setting": registry.ProjectSettingMCP, "value": registry.ProjectMCPOn})
	if got := projectTools(t, laptop, upstreamProject); got != registry.ProjectMCPOff {
		t.Fatalf("the laptop took a setting while the server was out of reach: %s", got)
	}
	laptop.gate.proxyDown.Store(false)
	server.gate.proxyDown.Store(false)
	for name, node := range map[string]*federationNode{"laptop": laptop, "server": server} {
		testkit.Eventually(t, name+" takes the later setting", 15*time.Second, func() bool {
			return projectTools(t, node, upstreamProject) == registry.ProjectMCPOn
		})
	}
}

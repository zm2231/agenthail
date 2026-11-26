package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

var projectSettingWords = map[string]map[string]string{
	registry.ProjectSettingMCP: {
		registry.ProjectMCPOn:  "Claude sessions started outside Agenthail get the Agenthail tools",
		registry.ProjectMCPOff: "Claude sessions started outside Agenthail get no Agenthail tools",
	},
	registry.ProjectSettingLimitResume: {
		registry.ProjectLimitResumeHost: "sessions a usage limit stopped resume as each host's own setting says",
		registry.ProjectLimitResumeOn:   "sessions a usage limit stopped resume once it resets",
		registry.ProjectLimitResumeOff:  "sessions a usage limit stopped are not resumed",
	},
}

func (a *App) cmdProjectSetting(ctx context.Context, client *daemonAPIClient, positional []string, jsonOut bool) error {
	if len(positional) != 3 || projectSettingWords[positional[0]][positional[2]] == "" {
		return fmt.Errorf("%s", projectUsage)
	}
	target, err := a.settingTarget(ctx, client, positional[1])
	if err != nil {
		return err
	}
	var answer struct {
		Setting struct {
			Project string `json:"project"`
			Name    string `json:"name"`
			Value   string `json:"value"`
		} `json:"setting"`
	}
	if err := a.gatedProjectAction(ctx, client, map[string]any{"action": "project-setting", "projectId": target.ID, "setting": positional[0], "value": positional[2]}, &answer); err != nil {
		return err
	}
	if jsonOut {
		return emit(answer)
	}
	where := "on this host"
	if registry.SharedProjectID(answer.Setting.Project) {
		where = "on every paired host"
	}
	fmt.Printf("%s: %s, %s\n", target.DisplayName, projectSettingWords[answer.Setting.Name][answer.Setting.Value], where)
	return nil
}

func (a *App) settingTarget(ctx context.Context, client *daemonAPIClient, selector string) (projectRef, error) {
	if hosts.ProjectKey(selector) {
		return projectRef{ID: selector, DisplayName: selector}, nil
	}
	if info, err := os.Stat(selector); err == nil && info.IsDir() {
		folder, err := filepath.Abs(selector)
		if err != nil {
			return projectRef{}, err
		}
		found, err := a.projectOf(folder)
		if err != nil {
			return projectRef{}, err
		}
		return projectRef{ID: found.ID, DisplayName: found.DisplayName}, nil
	}
	projects, err := daemonProjects(ctx, client)
	if err != nil {
		return projectRef{}, err
	}
	project, err := resolveProject(projects, selector)
	if err != nil {
		return projectRef{}, err
	}
	return projectRef{ID: project.ID, DisplayName: project.DisplayName}, nil
}

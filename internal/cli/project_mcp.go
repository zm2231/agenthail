package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
)

func (a *App) cmdProjectMCP(ctx context.Context, client *daemonAPIClient, positional []string, jsonOut bool) error {
	if len(positional) != 3 || (positional[2] != "on" && positional[2] != "off") {
		return fmt.Errorf("%s", projectUsage)
	}
	target, err := a.settingTarget(ctx, client, positional[1])
	if err != nil {
		return err
	}
	var answer struct {
		Setting struct {
			Project string `json:"project"`
			Value   string `json:"value"`
		} `json:"setting"`
	}
	if err := projectAction(ctx, client, map[string]any{"action": "project-setting", "projectId": target.ID, "setting": "mcp", "value": positional[2]}, &answer); err != nil {
		return err
	}
	if jsonOut {
		return emit(answer)
	}
	what := map[string]string{"off": "get no Agenthail tools", "on": "get the Agenthail tools"}[answer.Setting.Value]
	where := "on this host"
	if registry.SharedProjectID(answer.Setting.Project) {
		where = "on every paired host"
	}
	fmt.Printf("%s: Claude sessions started outside Agenthail %s, %s\n", target.DisplayName, what, where)
	return nil
}

func (a *App) settingTarget(ctx context.Context, client *daemonAPIClient, selector string) (projectRef, error) {
	if strings.HasPrefix(selector, hosts.RepositoryPrefix) {
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

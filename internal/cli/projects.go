package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const projectUsage = `usage: agenthail project list [--all] [--json]
       agenthail project add <path> [--host <host>] [--json]
       agenthail project <archive|unarchive> <name|id> [--json]

A Git repository is one project, including its worktrees and subfolders. A
folder outside Git becomes a project once it is added. Archiving hides a
project and its sessions until it is unarchived or one of its sessions shows
new activity.`

type projectHost struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

type projectRow struct {
	ID           string       `json:"id"`
	DisplayName  string       `json:"displayName"`
	RootPath     string       `json:"rootPath"`
	Kind         string       `json:"kind"`
	Host         *projectHost `json:"host,omitempty"`
	LastActive   time.Time    `json:"lastActive,omitempty"`
	SessionCount int          `json:"sessionCount"`
	Archived     bool         `json:"archived"`
	Coordinator  *struct {
		Name string `json:"name"`
	} `json:"coordinator,omitempty"`
}

func (p projectRow) hostName() string {
	if p.Host == nil {
		return ""
	}
	return p.Host.Name
}

func (a *App) cmdProject(args []string) error {
	positional := stripFlags(args)
	if hasFlag(args, "--help") || len(positional) == 0 {
		fmt.Println(projectUsage)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.DefaultTimeout)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	jsonOut := hasFlag(args, "--json")
	switch positional[0] {
	case "list", "ls":
		projects, err := daemonProjects(ctx, client)
		if err != nil {
			return err
		}
		if !hasFlag(args, "--all") {
			visible := projects[:0]
			for _, project := range projects {
				if !project.Archived {
					visible = append(visible, project)
				}
			}
			projects = visible
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(map[string]any{"projects": projects})
		}
		if len(projects) == 0 {
			fmt.Println("no projects found")
			return nil
		}
		fmt.Printf("%-24s %-12s %-8s %-10s %-24s %s\n", "PROJECT", "HOST", "SESSIONS", "LAST", "LEAD", "ROOT")
		for _, project := range projects {
			name := project.DisplayName
			if project.Archived {
				name += " (archived)"
			}
			lead := "-"
			if project.Coordinator != nil {
				lead = "@" + project.Coordinator.Name
			}
			fmt.Printf("%-24s %-12s %-8d %-10s %-24s %s\n", truncate(name, 24), truncate(project.hostName(), 12), project.SessionCount, relTime(project.LastActive), truncate(lead, 24), project.RootPath)
		}
		return nil
	case "add":
		if len(positional) != 2 {
			return fmt.Errorf("%s", projectUsage)
		}
		payload := map[string]any{"action": "project-add", "path": positional[1]}
		if host := strings.TrimSpace(flagVal(args, "--host")); host != "" {
			payload["host"] = host
		} else if absolute, err := filepath.Abs(positional[1]); err == nil {
			payload["path"] = absolute
		}
		var answer struct {
			Project projectRow `json:"project"`
		}
		if err := projectAction(ctx, client, payload, &answer); err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(answer)
		}
		if answer.Project.Kind == "git" {
			fmt.Printf("%s is part of the Git project %s (%s)\n", positional[1], answer.Project.DisplayName, answer.Project.RootPath)
			return nil
		}
		fmt.Printf("added project %s (%s)\n", answer.Project.DisplayName, answer.Project.RootPath)
		return nil
	case "archive", "unarchive":
		if len(positional) != 2 {
			return fmt.Errorf("%s", projectUsage)
		}
		projects, err := daemonProjects(ctx, client)
		if err != nil {
			return err
		}
		project, err := resolveProject(projects, positional[1])
		if err != nil {
			return err
		}
		var answer map[string]any
		if err := projectAction(ctx, client, map[string]any{"action": "project-" + positional[0], "projectId": project.ID}, &answer); err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(answer)
		}
		fmt.Printf("%sd project %s\n", positional[0], project.DisplayName)
		return nil
	default:
		return fmt.Errorf("%s", projectUsage)
	}
}

func daemonProjects(ctx context.Context, client *daemonAPIClient) ([]projectRow, error) {
	var snapshot struct {
		Projects []projectRow `json:"projects"`
	}
	if err := client.getJSON(ctx, "/api/v1/snapshot", nil, &snapshot); err != nil {
		return nil, fmt.Errorf("read projects: %w", err)
	}
	return snapshot.Projects, nil
}

// resolveProject matches a project id exactly, then a display name ignoring
// case. A name shared by several projects is an error, never a pick.
func resolveProject(projects []projectRow, selector string) (projectRow, error) {
	var named []projectRow
	for _, project := range projects {
		if project.ID == selector {
			return project, nil
		}
		if strings.EqualFold(project.DisplayName, selector) {
			named = append(named, project)
		}
	}
	switch len(named) {
	case 0:
		return projectRow{}, fmt.Errorf("no project matched %q; see 'agenthail project list --all'", selector)
	case 1:
		return named[0], nil
	}
	labels := make([]string, 0, len(named))
	for _, project := range named {
		labels = append(labels, fmt.Sprintf("%s on %s (%s)", project.RootPath, project.hostName(), project.ID))
	}
	return projectRow{}, fmt.Errorf("project %q is ambiguous: %s; use its id", selector, strings.Join(labels, ", "))
}

func projectAction(ctx context.Context, client *daemonAPIClient, payload map[string]any, answer any) error {
	data, status, err := client.action(ctx, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return fmt.Errorf("%s", strings.TrimSpace(string(data)))
	}
	return json.Unmarshal(data, answer)
}

func (a *App) cmdThreadArchive(args []string) error {
	positional := stripFlags(args)
	if len(positional) != 2 {
		return fmt.Errorf("usage: agenthail thread <archive|unarchive> <target> [--json]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.DefaultTimeout)
	defer cancel()
	session, _, remote, err := a.resolveAnyTarget(ctx, positional[1])
	if err != nil {
		return err
	}
	id := ""
	if remote != nil {
		id = remote.ID
	} else {
		id = session.ID
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	var answer map[string]any
	if err := projectAction(ctx, client, map[string]any{"action": "session-" + positional[0], "sessionId": id}, &answer); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(answer)
	}
	fmt.Printf("%sd %s\n", positional[0], positional[1])
	return nil
}

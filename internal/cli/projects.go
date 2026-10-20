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
       agenthail project add <path> [--name <title>] [--host <host>] [--json]
       agenthail project remove <name|id|path> [--json]
       agenthail project <archive|unarchive> <name|id|path> [--json]

A project is a folder you add, and it may be a folder inside a repository. A
session belongs to the deepest added project that holds its folder, reading a
worktree as its main checkout. A repository with no added project around the
session is a project of its own. Projects in the same place of a repository
with a remote are one project on every paired host. Archiving hides a project
and its sessions until it is unarchived or one of its sessions shows new
activity; removing an added project moves its sessions to the next project
that holds them.`

type projectHost struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	State string `json:"state,omitempty"`
}

type projectCheckout struct {
	Host         projectHost `json:"host"`
	ProjectID    string      `json:"projectId"`
	RootPath     string      `json:"rootPath"`
	Added        bool        `json:"added"`
	SessionCount int         `json:"sessionCount"`
	Archived     bool        `json:"archived"`
}

type projectCoordinator struct {
	Host      projectHost `json:"host"`
	Name      string      `json:"name"`
	Surface   string      `json:"surface"`
	SessionID string      `json:"sessionId,omitempty"`
	ChangedBy string      `json:"changedBy,omitempty"`
	ChangedAt time.Time   `json:"changedAt"`
}

type projectRow struct {
	ID           string               `json:"id"`
	DisplayName  string               `json:"displayName"`
	Kind         string               `json:"kind"`
	Checkouts    []projectCheckout    `json:"checkouts"`
	Coordinators []projectCoordinator `json:"coordinators"`
	LastActive   time.Time            `json:"lastActive,omitempty"`
	SessionCount int                  `json:"sessionCount"`
	Archived     bool                 `json:"archived"`
}

// lead names the project's coordinator as this host addresses it, "-" when it
// has none, and marks a conflict when several hosts each have one;
// 'coordinator list' names them.
func (p projectRow) lead() string {
	refs := make([]string, 0, len(p.Coordinators))
	for _, coordinator := range p.Coordinators {
		ref := "@" + coordinator.Name
		if coordinator.Host.State != "local" {
			ref = coordinator.Host.Name + "::" + ref
		}
		refs = append(refs, ref)
	}
	switch len(refs) {
	case 0:
		return "-"
	case 1:
		return refs[0]
	default:
		return fmt.Sprintf("conflict (%d)", len(refs))
	}
}

func (p projectRow) hostNames() string {
	names := make([]string, 0, len(p.Checkouts))
	for _, checkout := range p.Checkouts {
		names = append(names, checkout.Host.Name)
	}
	return strings.Join(names, ",")
}

// checkoutNamed finds the checkout a selector names by its id, or by its
// folder on this host.
func (p projectRow) checkoutNamed(selector string) (projectCheckout, bool) {
	folder, _ := filepath.Abs(selector)
	for _, checkout := range p.Checkouts {
		if checkout.ProjectID == selector || (checkout.Host.State == "local" && checkout.RootPath == folder) {
			return checkout, true
		}
	}
	return projectCheckout{}, false
}

// rootPath is this host's checkout, else the first one listed.
func (p projectRow) rootPath() string {
	for _, checkout := range p.Checkouts {
		if checkout.Host.State == "local" {
			return checkout.RootPath
		}
	}
	if len(p.Checkouts) == 0 {
		return ""
	}
	return p.Checkouts[0].RootPath
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
		fmt.Printf("%-24s %-20s %-24s %-8s %-10s %s\n", "PROJECT", "HOSTS", "LEAD", "SESSIONS", "LAST", "ROOT")
		for _, project := range projects {
			name := project.DisplayName
			if project.Archived {
				name += " (archived)"
			}
			fmt.Printf("%-24s %-20s %-24s %-8d %-10s %s\n", truncate(name, 24), truncate(project.hostNames(), 20), truncate(project.lead(), 24), project.SessionCount, relTime(project.LastActive), project.rootPath())
		}
		return nil
	case "add":
		if len(positional) != 2 {
			return fmt.Errorf("%s", projectUsage)
		}
		payload := map[string]any{"action": "project-add", "path": positional[1]}
		if title := strings.TrimSpace(flagVal(args, "--name")); title != "" {
			payload["title"] = title
		}
		if host := strings.TrimSpace(flagVal(args, "--host")); host != "" {
			payload["host"] = host
		} else if absolute, err := filepath.Abs(positional[1]); err == nil {
			payload["path"] = absolute
		}
		var answer struct {
			Project struct {
				DisplayName string `json:"displayName"`
				RootPath    string `json:"rootPath"`
				Kind        string `json:"kind"`
			} `json:"project"`
		}
		if err := projectAction(ctx, client, payload, &answer); err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(answer)
		}
		fmt.Printf("added project %s (%s)\n", answer.Project.DisplayName, answer.Project.RootPath)
		return nil
	case "remove", "rm":
		if len(positional) != 2 {
			return fmt.Errorf("%s", projectUsage)
		}
		projects, err := daemonProjects(ctx, client)
		if err != nil {
			return err
		}
		checkout, err := resolveAddedCheckout(projects, positional[1])
		if err != nil {
			return err
		}
		var answer map[string]any
		if err := projectAction(ctx, client, map[string]any{"action": "project-remove", "projectId": checkout.ProjectID}, &answer); err != nil {
			return err
		}
		if jsonOut {
			return json.NewEncoder(os.Stdout).Encode(answer)
		}
		fmt.Printf("removed project %s on %s\n", checkout.RootPath, checkout.Host.Name)
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
		target := project.ID
		if checkout, found := project.checkoutNamed(positional[1]); found {
			target = checkout.ProjectID
		}
		if err := projectAction(ctx, client, map[string]any{"action": "project-" + positional[0], "projectId": target}, &answer); err != nil {
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

// resolveProject matches a project id or one of its checkouts' ids exactly,
// then this host's checkout folder, then a display name ignoring case. A name
// shared by several projects is an error, never a pick.
func resolveProject(projects []projectRow, selector string) (projectRow, error) {
	for _, project := range projects {
		if project.ID == selector {
			return project, nil
		}
		if _, found := project.checkoutNamed(selector); found {
			return project, nil
		}
	}
	var named []projectRow
	for _, project := range projects {
		if strings.EqualFold(project.DisplayName, selector) {
			named = append(named, project)
		}
	}
	switch len(named) {
	case 0:
		return projectRow{}, fmt.Errorf("no project matched %q; a project's id can change, for example after its repository's origin changed; see 'agenthail project list --all' for the current ones", selector)
	case 1:
		return named[0], nil
	}
	labels := make([]string, 0, len(named))
	for _, project := range named {
		labels = append(labels, fmt.Sprintf("%s on %s (%s)", project.rootPath(), project.hostNames(), project.ID))
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

// resolveAddedCheckout picks the one added checkout a selector names: the
// checkout itself by id or folder, else the project's only added checkout,
// preferring this host's.
func resolveAddedCheckout(projects []projectRow, selector string) (projectCheckout, error) {
	project, err := resolveProject(projects, selector)
	if err != nil {
		return projectCheckout{}, err
	}
	if checkout, found := project.checkoutNamed(selector); found && checkout.Added {
		return checkout, nil
	}
	var local, remote []projectCheckout
	for _, checkout := range project.Checkouts {
		if !checkout.Added {
			continue
		}
		if checkout.Host.State == "local" {
			local = append(local, checkout)
		} else {
			remote = append(remote, checkout)
		}
	}
	candidates := local
	if len(candidates) == 0 {
		candidates = remote
	}
	switch len(candidates) {
	case 0:
		return projectCheckout{}, fmt.Errorf("project %s was not added, so it cannot be removed; archive it to hide it", project.DisplayName)
	case 1:
		return candidates[0], nil
	}
	labels := make([]string, 0, len(candidates))
	for _, checkout := range candidates {
		labels = append(labels, fmt.Sprintf("%s on %s (%s)", checkout.RootPath, checkout.Host.Name, checkout.ProjectID))
	}
	return projectCheckout{}, fmt.Errorf("project %s is added in several folders: %s; remove one by its id or folder", project.DisplayName, strings.Join(labels, ", "))
}

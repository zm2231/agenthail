package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
)

type projectRef struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

type projectLinkAnswer struct {
	OK      bool `json:"ok"`
	Changed bool `json:"changed"`
	Link    struct {
		Joined   string `json:"joined"`
		Survivor string `json:"survivor"`
		Linked   bool   `json:"linked"`
	} `json:"link"`
}

func (a *App) cmdProjectLink(ctx context.Context, client *daemonAPIClient, positional []string, jsonOut bool) error {
	unlink := positional[0] == "unlink"
	if (unlink && len(positional) != 2) || (!unlink && len(positional) != 3) {
		return fmt.Errorf("%s", projectUsage)
	}
	projects, err := daemonProjects(ctx, client)
	if err != nil {
		return err
	}
	joined, err := a.linkTarget(projects, positional[1])
	if err != nil {
		return err
	}
	payload := map[string]any{"action": "project-unlink", "projectId": joined.ID}
	var survivor projectRef
	if !unlink {
		if survivor, err = a.linkTarget(projects, positional[2]); err != nil {
			return err
		}
		payload = map[string]any{"action": "project-link", "projectId": joined.ID, "survivor": survivor.ID}
	}
	var answer projectLinkAnswer
	if err := projectAction(ctx, client, payload, &answer); err != nil {
		return err
	}
	if jsonOut {
		return emit(answer)
	}
	switch {
	case unlink:
		fmt.Printf("unlinked %s; it is a project of its own again\n", joined.DisplayName)
	case !answer.Changed:
		fmt.Printf("%s is already linked into %s\n", joined.DisplayName, survivor.DisplayName)
	default:
		fmt.Printf("linked %s into %s; its sessions, lead and ledger are %s's on every paired host\n", joined.DisplayName, survivor.DisplayName, survivor.DisplayName)
	}
	return nil
}

func (p projectRow) linkNotes() []string {
	var notes []string
	if len(p.Linked) > 0 {
		names := make([]string, 0, len(p.Linked))
		for _, linked := range p.Linked {
			names = append(names, linked.DisplayName)
		}
		notes = append(notes, "includes "+strings.Join(names, ", "))
	}
	if p.LinkHeld != "" {
		notes = append(notes, "link held: "+p.LinkHeld)
	}
	for _, same := range p.SameHistory {
		notes = append(notes, fmt.Sprintf("same history as %s; to make them one project: agenthail project link %s %s", same.DisplayName, p.linkSelector(), same.ID))
	}
	return notes
}

// doctorProjectNotes lists every project's link notes, and counts the links
// this host holds without applying, which leave hosts disagreeing about the
// project.
func (a *App) doctorProjectNotes(ctx context.Context) ([]string, int, error) {
	client, err := a.daemonAPI()
	if err != nil {
		return nil, 0, err
	}
	projects, err := daemonProjects(ctx, client)
	if err != nil {
		return nil, 0, err
	}
	notes, held := []string{}, 0
	for _, project := range projects {
		if project.LinkHeld != "" {
			held++
		}
		for _, note := range project.linkNotes() {
			notes = append(notes, project.DisplayName+": "+note)
		}
	}
	return notes, held, nil
}

func (p projectRow) linkSelector() string {
	for _, checkout := range p.Checkouts {
		if checkout.Host.State == "local" && checkout.LinkedFrom == nil {
			return checkout.RootPath
		}
	}
	return p.ID
}

// linkTarget names the project a selector means before any link joins it,
// so a linked checkout's folder still names its own project.
func (a *App) linkTarget(projects []projectRow, selector string) (projectRef, error) {
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
		if found.LinkedFrom != nil {
			return projectRef{ID: found.LinkedFrom.ID, DisplayName: found.LinkedFrom.DisplayName}, nil
		}
		return projectRef{ID: found.ID, DisplayName: found.DisplayName}, nil
	}
	for _, project := range projects {
		for _, linked := range project.Linked {
			if linked.ID == selector || strings.EqualFold(linked.DisplayName, selector) {
				return linked, nil
			}
		}
	}
	project, err := resolveProject(projects, selector)
	if err != nil {
		return projectRef{}, err
	}
	return projectRef{ID: project.ID, DisplayName: project.DisplayName}, nil
}

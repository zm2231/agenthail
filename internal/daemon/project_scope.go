package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/registry"
)

var ErrNoProject = errors.New("no project")

type scopedProject struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	RootPath    string `json:"rootPath"`
}

type SessionProjects map[string]scopedProject

func CatalogSessionProjects(reg *registry.Registry) (SessionProjects, error) {
	snapshot, err := reg.CatalogSnapshot()
	if err != nil {
		return nil, fmt.Errorf("read daemon catalog: %w", err)
	}
	projects := make(SessionProjects, len(snapshot.Sessions))
	for _, record := range snapshot.Sessions {
		var view struct {
			Project *scopedProject `json:"project"`
		}
		if json.Unmarshal([]byte(record.ProjectionFingerprint), &view) == nil && view.Project != nil {
			projects[record.Session.ID] = *view.Project
		}
	}
	return projects, nil
}

// A display name shared by several projects is an error, never a pick.
func (p SessionProjects) Named(selector string) (string, error) {
	named := map[string]scopedProject{}
	for _, project := range p {
		if project.ID == selector {
			return selector, nil
		}
		if strings.EqualFold(project.DisplayName, selector) {
			named[project.ID] = project
		}
	}
	switch len(named) {
	case 0:
		return "", fmt.Errorf("%w matched %q; see 'agenthail project list --all'", ErrNoProject, selector)
	case 1:
		for id := range named {
			return id, nil
		}
	}
	labels := make([]string, 0, len(named))
	for id, project := range named {
		labels = append(labels, fmt.Sprintf("%s (%s)", project.RootPath, id))
	}
	sort.Strings(labels)
	return "", fmt.Errorf("project %q is ambiguous: %s; use its id", selector, strings.Join(labels, ", "))
}

func (p SessionProjects) In(rows []registry.QueueRow, projectID string) []registry.QueueRow {
	kept := make([]registry.QueueRow, 0, len(rows))
	for _, row := range rows {
		if p[row.SessionID].ID == projectID {
			kept = append(kept, row)
		}
	}
	return kept
}

// ProjectHolding is the project a session working in folder would join, so
// a worktree counts for the project its folder belongs to.
func ProjectHolding(ctx context.Context, reg *registry.Registry, folder string) (string, error) {
	if !filepath.IsAbs(folder) {
		return "", fmt.Errorf("a folder must be an absolute path, not %q", folder)
	}
	found, err := ResolvePathProject(ctx, reg, folder)
	if err != nil {
		return "", err
	}
	if found == nil {
		return "", fmt.Errorf("%w holds %s; add it with 'agenthail project add %s'", ErrNoProject, folder, folder)
	}
	return found.ID, nil
}

package projectresolve

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
)

// ResolvePathProject finds the project a session working in path belongs
// to, resolved the way the catalog resolves sessions. It is nil when no
// project holds the folder.
func ResolvePathProject(ctx context.Context, reg *registry.Registry, path string) (*CatalogProject, error) {
	path, err := catalogSessionPath(path)
	if err != nil || path == "" {
		return nil, fmt.Errorf("a folder path is required")
	}
	records, err := reg.ListProjects()
	if err != nil {
		return nil, err
	}
	added := records[:0]
	for _, record := range records {
		if record.Added {
			added = append(added, record)
		}
	}
	links, err := LoadProjectLinks(reg)
	if err != nil {
		return nil, err
	}
	identity, _, transient := resolveCatalogIdentity(ctx, path, added, links, nil)
	if transient {
		return nil, fmt.Errorf("resolve the project of %s: %s", path, identity.Problem)
	}
	return identity.Project, nil
}

// The stored project survives a transient git failure, as the daemon's own resolution does.
func StoredSessionProject(reg *registry.Registry, sessionID string) *CatalogProject {
	raw, _, err := reg.CatalogSessionProject(sessionID)
	if err != nil || len(raw) == 0 {
		return nil
	}
	var project *CatalogProject
	if json.Unmarshal(raw, &project) != nil || project == nil || project.ID == "" || project.LocalID == "" {
		return nil
	}
	return project
}

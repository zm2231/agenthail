package daemon

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
)

// LedgerKey names a resolved project's ledger: its repository key, or for an
// added project without one, the key of its own id. A ledger already kept
// under an added project's id stays there once the project gains a
// repository, for every checkout and link that resolves to that project.
// A folder whose removed project kept a ledger has none until it is added
// again, rather than a new one under its repository.
func LedgerKey(reg *registry.Registry, found *CatalogProject) (string, error) {
	if found == nil {
		return "", nil
	}
	records, err := reg.ListProjects()
	if err != nil {
		return "", err
	}
	if found.LinkedFrom == nil {
		for _, record := range records {
			if record.Added && record.ID == found.LocalID {
				return addedLedgerKey(reg, record)
			}
		}
	}
	for _, record := range records {
		if !record.Added || record.ID != found.ID && (record.Key == "" || record.Key != found.ID) {
			continue
		}
		if own := registry.LedgerProjectKey(record.ID); record.ID != found.ID {
			if held, err := reg.HoldsLedger(own); err != nil || held {
				return own, err
			}
			continue
		}
		return addedLedgerKey(reg, record)
	}
	if _, kept, err := reg.RemovedProjectLedger(found.RootPath); err != nil || kept {
		return "", err
	}
	return found.Key, nil
}

func addedLedgerKey(reg *registry.Registry, record registry.ProjectRecord) (string, error) {
	own := registry.LedgerProjectKey(record.ID)
	if held, err := reg.HoldsLedger(own); err != nil || held {
		return own, err
	}
	if record.Key != "" {
		return record.Key, nil
	}
	return own, nil
}

func ResolvePathLedgerKey(ctx context.Context, reg *registry.Registry, path string) (string, error) {
	project, err := ResolvePathProject(ctx, reg, path)
	if err != nil {
		return "", err
	}
	key, err := LedgerKey(reg, project)
	if err != nil || key != "" {
		return key, err
	}
	root := path
	if project != nil {
		root = project.RootPath
	}
	if id, kept, err := reg.RemovedProjectLedger(root); err != nil {
		return "", err
	} else if kept {
		return "", fmt.Errorf("%s is no longer an added project; its ledger (%s) comes back when you add it again with 'agenthail project add'", root, registry.LedgerProjectKey(id))
	}
	return "", fmt.Errorf("no project found for %s; add it with 'agenthail project add'", path)
}

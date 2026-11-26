package projectresolve

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
)

// LedgerKey names a resolved project's ledger: its repository key, or for an
// added project without one, the key of its own id. A ledger already kept
// under an added project's id stays there once the project gains a
// repository, for every checkout and link that resolves to that project.
func LedgerKey(reg *registry.Registry, found *CatalogProject) (string, error) {
	if found == nil {
		return "", nil
	}
	keys, err := NewLedgerKeys(reg)
	if err != nil {
		return "", err
	}
	return keys.Of(found)
}

// LedgerKeys resolves many projects' ledger keys against one read of the
// added projects and the repository moves.
type LedgerKeys struct {
	reg     *registry.Registry
	records []registry.ProjectRecord
	moved   map[string]string
}

func NewLedgerKeys(reg *registry.Registry) (*LedgerKeys, error) {
	records, err := reg.ListProjects()
	if err != nil {
		return nil, err
	}
	moved, err := reg.ProjectMoveTargets()
	if err != nil {
		return nil, err
	}
	for index := range records {
		if to, found := moved[records[index].Key]; found {
			records[index].Key = to
		}
	}
	return &LedgerKeys{reg: reg, records: records, moved: moved}, nil
}

// Records are the projects added here, their keys following repository moves.
func (k *LedgerKeys) Records() []registry.ProjectRecord { return k.records }

func (k *LedgerKeys) Of(found *CatalogProject) (string, error) {
	if found == nil {
		return "", nil
	}
	reg := k.reg
	if found.LinkedFrom == nil {
		for _, record := range k.records {
			if record.Added && record.ID == found.LocalID {
				return addedLedgerKey(reg, record)
			}
		}
	}
	for _, record := range k.records {
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
	if to, found := k.moved[found.Key]; found {
		return to, nil
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

// ResolvePathLedgerKey is the ledger the person means in path. A folder
// whose removed project kept a ledger names that ledger until it is added
// again, unless an added project lies deeper.
func ResolvePathLedgerKey(ctx context.Context, reg *registry.Registry, path string) (string, error) {
	project, err := ResolvePathProject(ctx, reg, path)
	if err != nil {
		return "", err
	}
	folder, err := catalogSessionPath(path)
	if err != nil {
		return "", err
	}
	removedRoot, removedID, kept, err := reg.RemovedProjectLedgerAt(folder)
	if err != nil {
		return "", err
	}
	if kept && (project == nil || !project.Added || !catalogWithin(removedRoot, project.RootPath) || project.RootPath == removedRoot) {
		return "", fmt.Errorf("%s is no longer an added project; its ledger (%s) comes back when you add it again with 'agenthail project add'", removedRoot, registry.LedgerProjectKey(removedID))
	}
	key, err := LedgerKey(reg, project)
	if err != nil || key != "" {
		return key, err
	}
	return "", fmt.Errorf("no project found for %s; add it with 'agenthail project add'", path)
}

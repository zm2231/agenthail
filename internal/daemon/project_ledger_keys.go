package daemon

import (
	"encoding/json"
	"strings"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

// projectLedgerKeys resolves any id a project is listed under (the snapshot's
// project id, a checkout's local id, bare or qualified by this host) to the
// key its ledger is kept under, the ledger its sessions write to.
type projectLedgerKeys struct {
	keys         *projectresolve.LedgerKeys
	links        projectresolve.ProjectLinkSet
	local        map[string]*projectresolve.CatalogProject
	remote       map[string]*projectresolve.CatalogProject
	coordinators []registry.CoordinatorSpec
	hostID       string
}

func (d *Daemon) projectLedgerKeys() (*projectLedgerKeys, error) {
	keys, err := projectresolve.NewLedgerKeys(d.Registry)
	if err != nil {
		return nil, err
	}
	links, err := projectresolve.LoadProjectLinks(d.Registry)
	if err != nil {
		return nil, err
	}
	sightings, err := d.Registry.CatalogProjectSightings()
	if err != nil {
		return nil, err
	}
	coordinators, err := d.Registry.Coordinators()
	if err != nil {
		return nil, err
	}
	resolver := &projectLedgerKeys{keys: keys, links: links, local: map[string]*projectresolve.CatalogProject{}, remote: map[string]*projectresolve.CatalogProject{}, coordinators: coordinators, hostID: d.hostIdentity().ID}
	for _, sighting := range sightings {
		var found projectresolve.CatalogProject
		if json.Unmarshal(sighting.Project, &found) != nil || found.ID == "" {
			continue
		}
		seen := resolver.local
		if sighting.Remote {
			seen = resolver.remote
		}
		for _, id := range []string{found.ID, found.LocalID} {
			if earlier, listed := seen[id]; id != "" && (!listed || sighting.Remote && earlier.Key == "" && found.Key != "") {
				seen[id] = &found
			}
		}
	}
	return resolver, nil
}

// of is the ledger key of the project listed under id; named is false when
// no project is listed under it. A project with no ledger, an implicit
// folder, is named with an empty key.
func (p *projectLedgerKeys) of(id string) (key string, named bool, err error) {
	id = strings.TrimSpace(id)
	if host, local, qualified := hosts.Split(id); qualified && host == p.hostID {
		id = local
	}
	if id == "" {
		return "", false, nil
	}
	if found := p.local[id]; found != nil {
		key, err := p.keys.Of(p.links.Apply(found))
		return key, true, err
	}
	for _, record := range p.keys.Records() {
		if record.Added && (record.ID == id || record.Key == id) {
			key, err := p.keys.Of(projectresolve.AddedCatalogProject(record, record.Key != ""))
			return key, true, err
		}
	}
	for _, spec := range p.coordinators {
		if lead := coordinatorCatalogProject(spec); spec.HostID == "" && (lead.ID == id || lead.LocalID == id) {
			key, err := p.keys.Of(p.links.Apply(lead))
			return key, true, err
		}
	}
	if found := p.remote[id]; found != nil {
		key, err := p.elsewhere(found.ID, found.Key)
		return key, true, err
	}
	for _, spec := range p.coordinators {
		if spec.HostID != "" && spec.ProjectID == id {
			key, err := p.elsewhere(id, "")
			return key, true, err
		}
	}
	if hosts.ProjectKey(id) {
		key, err := p.keys.Of(&projectresolve.CatalogProject{ID: id, Key: id})
		return key, true, err
	}
	return "", false, nil
}

// A project known only from a paired host carries no local record, so its
// key follows from what it is: its repository's key, none for an implicit
// folder, and for an added folder without one, its own id's.
func (p *projectLedgerKeys) elsewhere(id, key string) (string, error) {
	switch {
	case key != "":
	case hosts.ProjectKey(id):
		key = id
	case strings.HasPrefix(id, projectresolve.CatalogImplicitProjectID("")):
		return "", nil
	default:
		key = registry.LedgerProjectKey(id)
	}
	return p.keys.Of(&projectresolve.CatalogProject{ID: id, Key: key})
}

package daemon

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/zm2231/agenthail/internal/broadcast"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/projectresolve"
	"github.com/zm2231/agenthail/internal/registry"
)

const ledgerProjectsNamed = 5

// callerLedgerKey is the ledger a ledger tool works on: the caller's own
// project's, or the one it names, which outside its own project its role
// must reach.
func (d *Daemon) callerLedgerKey(caller *mcpCaller, selector string) (string, error) {
	own, err := projectresolve.LedgerKey(d.Registry, caller.project)
	if err != nil {
		return "", err
	}
	selector = strings.TrimSpace(selector)
	if selector == "" {
		if own != "" {
			return own, nil
		}
		if caller.project != nil {
			return "", refuse("invalid", "Your session is not in a project with a ledger; the person adds one with agenthail project add.")
		}
		return "", d.ledgerProjectRequired(caller)
	}
	named, err := broadcast.FindProject(d.Registry, selector)
	if err != nil {
		return "", refuse("not_found", err.Error()+"; agenthail_projects lists the projects.")
	}
	key, err := d.projectLedgerKey(named)
	if err != nil {
		return "", err
	}
	if key == "" {
		return "", refuse("not_found", named.DisplayName+" has no ledger.")
	}
	granted, err := d.ledgerGranted(caller, key)
	if err != nil {
		return "", err
	}
	if key == own || granted {
		return key, nil
	}
	if caller.role == nil {
		return "", refuse("capability_missing", "An extension works only on its own project's ledger.")
	}
	refusal := refuse("capability_missing", fmt.Sprintf("Your role, %s, reaches only your own project's ledger, not %s's; the person widens it with agenthail roles ledgers %s +%s.", caller.role.label(), named.DisplayName, caller.role.Name, named.ID))
	refusal.Detail = map[string]any{"rule": "role_ledgers", "role": caller.role.Name}
	return "", refusal
}

func (d *Daemon) ledgerProjectRequired(caller *mcpCaller) error {
	known, err := broadcast.Projects(d.Registry)
	if err != nil {
		return err
	}
	var names []string
	reached := 0
	for _, project := range known {
		if !caller.role.ReachesLedger(project.ID) {
			continue
		}
		reached++
		if len(names) < ledgerProjectsNamed {
			names = append(names, project.DisplayName)
		}
	}
	if reached == 0 {
		return refuse("invalid", "You are not in a project, and your role reaches no project's ledger; the person grants some with agenthail roles ledgers.")
	}
	list := strings.Join(names, ", ")
	if more := reached - len(names); more > 0 {
		list += fmt.Sprintf(" and %d more (agenthail_projects lists them)", more)
	}
	return refuse("invalid", "You are not in a project; name one in project, by name or id: "+list+".")
}

// projectLedgerKey is the ledger of a project a session here is in, or one
// added here. A project known only from a paired host carries no local
// record, so its key follows from what it is: its repository's key, none for
// an implicit folder, and for an added folder without one, its own id's.
func (d *Daemon) projectLedgerKey(named broadcast.Project) (string, error) {
	snapshot, err := d.Registry.CatalogSnapshot()
	if err != nil {
		return "", err
	}
	elsewhere := projectresolve.CatalogProject{ID: named.ID, DisplayName: named.DisplayName}
	for _, record := range snapshot.Sessions {
		var found projectresolve.CatalogProject
		if len(record.Project) == 0 || json.Unmarshal(record.Project, &found) != nil || found.ID != named.ID {
			continue
		}
		if record.HostID == "" {
			return projectresolve.LedgerKey(d.Registry, &found)
		}
		if found.Key != "" {
			elsewhere.Key = found.Key
		}
	}
	records, err := d.Registry.ListProjects()
	if err != nil {
		return "", err
	}
	for _, record := range records {
		if record.Added && (record.ID == named.ID || record.Key == named.ID) {
			return projectresolve.LedgerKey(d.Registry, projectresolve.AddedCatalogProject(record, record.Key != ""))
		}
	}
	switch {
	case elsewhere.Key != "":
	case hosts.ProjectKey(named.ID):
		elsewhere.Key = named.ID
	case strings.HasPrefix(named.ID, projectresolve.CatalogImplicitProjectID("")):
		return "", nil
	default:
		elsewhere.Key = registry.LedgerProjectKey(named.ID)
	}
	return projectresolve.LedgerKey(d.Registry, &elsewhere)
}

// ledgerGranted is whether the caller's role reaches the ledger kept under
// key as another project's.
func (d *Daemon) ledgerGranted(caller *mcpCaller, key string) (bool, error) {
	if key == "" || caller.role == nil {
		return false, nil
	}
	if caller.role.AllLedgers() {
		return true, nil
	}
	for _, id := range caller.role.Ledgers {
		named, err := broadcast.FindProject(d.Registry, id)
		if err != nil {
			continue
		}
		granted, err := d.projectLedgerKey(named)
		if err != nil {
			return false, err
		}
		if granted == key {
			return true, nil
		}
	}
	return false, nil
}

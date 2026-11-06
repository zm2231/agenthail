package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type ledgerAccess struct {
	grants    map[string]time.Time
	ancestors []registry.ForkAncestor
}

// A fork inherits its ancestors' grants and coordinator role from the fork's creation on.
func (d *Daemon) ledgerTargets(store *ledger.Store, session surface.Session) ([]ledgerTarget, ledgerAccess, error) {
	var targets []ledgerTarget
	listed := map[string]bool{}
	if session.Cwd != "" {
		project, err := d.sessionProject(session)
		if err != nil {
			return nil, ledgerAccess{}, err
		}
		key, err := LedgerKey(d.Registry, project)
		if err != nil {
			return nil, ledgerAccess{}, err
		}
		if key != "" {
			targets = append(targets, ledgerTarget{key: key, projectID: project.ID})
			listed[key] = true
		}
	}
	ancestors, err := d.Registry.ForkAncestors(session.ID)
	if err != nil {
		return nil, ledgerAccess{}, err
	}
	access := ledgerAccess{grants: map[string]time.Time{}, ancestors: ancestors}
	grant := func(sessionID string, floor time.Time) error {
		grants, err := store.SessionGrants(sessionID)
		if err != nil {
			return err
		}
		for _, g := range grants {
			at := latest(g.Since, floor)
			if known, ok := access.grants[g.ProjectKey]; !ok || at.Before(known) {
				access.grants[g.ProjectKey] = at
			}
			if !listed[g.ProjectKey] {
				targets = append(targets, ledgerTarget{key: g.ProjectKey})
				listed[g.ProjectKey] = true
			}
		}
		return nil
	}
	if err := grant(session.ID, time.Time{}); err != nil {
		return nil, ledgerAccess{}, err
	}
	for _, ancestor := range ancestors {
		if err := grant(ancestor.SessionID, ancestor.Since); err != nil {
			return nil, ledgerAccess{}, err
		}
	}
	return targets, access, nil
}

// ledgerScope is when the person's words to a session start counting in a project, whether the session is asked to attach them, and whether
// its turn is held at the end for words it left unattached.
type ledgerScope struct {
	since       time.Time
	inScope     bool
	attach      bool
	checkpoints bool
	hold        bool
}

// ledgerSince is the earliest grant that puts sessionID in the project's ledger scope. Enabling the project, granting the session or leading
// the project asks it to attach; only enabling or granting holds its turn.
func (d *Daemon) ledgerSince(store *ledger.Store, target ledgerTarget, sessionID string, access ledgerAccess) (ledgerScope, error) {
	captured, err := store.Scope(target.key)
	if err != nil || captured.State == ledger.LedgerDisabled {
		return ledgerScope{}, err
	}
	scope := ledgerScope{since: captured.Since, inScope: captured.State == ledger.LedgerEnabled}
	scope.attach, scope.hold = scope.inScope, scope.inScope
	consider := func(at time.Time) {
		if !scope.inScope || at.Before(scope.since) {
			scope.since, scope.inScope = at, true
		}
	}
	if at, ok := access.grants[target.key]; ok {
		consider(at)
		scope.attach, scope.hold = true, true
	}
	if target.projectID != "" {
		lineage := append([]registry.ForkAncestor{{SessionID: sessionID}}, access.ancestors...)
		for _, member := range lineage {
			at, coordinator, err := d.Registry.CoordinatorSince(target.projectID, member.SessionID)
			if err != nil {
				return ledgerScope{}, err
			}
			if coordinator {
				consider(latest(at, member.Since))
				scope.attach = true
			}
		}
	}
	if captured.Attach == ledger.AttachAtCheckpoints {
		scope.attach, scope.checkpoints = false, scope.attach
	}
	return scope, nil
}

func latest(a, b time.Time) time.Time {
	if b.After(a) {
		return b
	}
	return a
}

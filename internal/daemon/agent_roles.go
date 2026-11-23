package daemon

import (
	"encoding/json"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/registry"
)

// resolvedRole is the role a caller acts under, with where it came from.
type resolvedRole struct {
	Name    string
	BuiltIn bool
	// Edited marks a built-in role the person changed.
	Edited bool
	agentroles.Role
	// Source says why the caller holds the role.
	Source string
	// Replaces names the role the caller holds by what it is, when an
	// assignment gave it another.
	Replaces string
}

// A caller without a role, an extension, leaves every action to the person
// and sees every tool its own gate allows.
func (r *resolvedRole) Decision(name string) string {
	if r == nil {
		return agentroles.Ask
	}
	return r.Role.Decision(name)
}

func (r *resolvedRole) DepthLimit() int {
	if r == nil {
		return agentroles.DefaultDepth
	}
	return r.Role.DepthLimit()
}

func (r *resolvedRole) RunningLimit() int {
	if r == nil {
		return agentroles.DefaultRunning
	}
	return r.Role.RunningLimit()
}

func (r *resolvedRole) Sees(tool string) bool {
	return r == nil || r.Role.Sees(tool)
}

func (r *resolvedRole) ReachesLedger(projectID string) bool {
	return r != nil && r.Role.ReachesLedger(projectID)
}

func (r *resolvedRole) label() string {
	if r.Replaces == "" {
		return r.Name
	}
	return r.Replaces + " (replaced by " + r.Name + ")"
}

// agentRoles reads every role: the built-ins as the person left them and
// the roles the person made.
func (d *Daemon) agentRoles() (map[string]resolvedRole, error) {
	roles := map[string]resolvedRole{}
	for name, role := range agentroles.BuiltIn {
		roles[name] = resolvedRole{Name: name, BuiltIn: true, Role: role}
	}
	records, err := d.Registry.AgentRoles()
	if err != nil {
		return roles, err
	}
	for _, record := range records {
		builtIn, isBuiltIn := agentroles.BuiltIn[record.Name]
		if record.Deleted {
			continue
		}
		var role agentroles.Role
		if err := json.Unmarshal(record.Definition, &role); err != nil || role.Validate() != nil {
			d.log.Printf("agent role %s: its stored definition is unreadable; using %s", record.Name, map[bool]string{true: "the built-in one", false: "nothing"}[isBuiltIn])
			continue
		}
		if isBuiltIn {
			role.Description = builtIn.Description
		}
		roles[record.Name] = resolvedRole{Name: record.Name, BuiltIn: isBuiltIn, Edited: isBuiltIn, Role: role}
	}
	return roles, nil
}

// implicitRole names the role a caller holds by what it is.
func implicitRole(roles registry.SessionRoles, remote bool) (string, string) {
	switch {
	case remote:
		return agentroles.Remote, "a remote identity"
	case roles.Operator:
		return agentroles.Operator, "the voice operator"
	case roles.Lead:
		return agentroles.Lead, "its project's coordinator"
	}
	return agentroles.Worker, "the default"
}

func startedOutside(record registry.CallerRecord) bool {
	return record.Session.ID != "" && !record.Session.Delegated() && !record.Originated
}

// callerRole resolves in a fixed order: an assignment to the session or to a
// conversation it was branched from, then to its identity, then to its alias,
// and last the role it holds by what it is. An assignment naming a role that
// no longer exists is passed over, which every host does alike once their
// roles and assignments have synced.
func (d *Daemon) callerRole(record registry.CallerRecord, remote bool) resolvedRole {
	implicit, source := implicitRole(record.Roles, remote)
	if implicit == agentroles.Worker && startedOutside(record) {
		source = "a session Agenthail did not start"
	}
	all, err := d.agentRoles()
	if err != nil {
		d.log.Printf("read agent roles: %s", err)
	}
	role := all[implicit]
	role.Source = source
	assignments, err := d.Registry.AgentRoleAssignments()
	if err != nil {
		d.log.Printf("read agent role assignments: %s", err)
		return role
	}
	var chain []string
	if record.Session.ID != "" {
		if chain, err = d.Registry.ForkChain(record.Session.ID); err != nil {
			d.log.Printf("read the branches of %s: %s", record.Session.ID, err)
			chain = []string{record.Session.ID}
		}
	}
	assigned, found := assignedRole(assignments, all, chain, record.Identity, record.Alias)
	if !found {
		return role
	}
	if assigned.Name != implicit {
		assigned.Replaces = implicit
	}
	return assigned
}

type assignmentKey struct{ kind, target string }

// assignedRole is the first live assignment in resolution order whose role exists.
func assignedRole(assignments []registry.AgentRoleAssignment, roles map[string]resolvedRole, chain []string, identity, alias string) (resolvedRole, bool) {
	live := map[assignmentKey]string{}
	for _, assignment := range assignments {
		if !assignment.Deleted {
			live[assignmentKey{assignment.Kind, assignment.Target}] = assignment.Role
		}
	}
	var order []assignmentKey
	for _, id := range chain {
		order = append(order, assignmentKey{registry.AssignSession, id})
	}
	if identity != "" {
		order = append(order, assignmentKey{registry.AssignIdentity, identity})
	}
	if alias != "" {
		order = append(order, assignmentKey{registry.AssignAlias, alias})
	}
	for i, key := range order {
		name, ok := live[key]
		if !ok {
			continue
		}
		role, exists := roles[name]
		if !exists {
			continue
		}
		role.Source = assignmentSource(key, i > 0 && key.kind == registry.AssignSession)
		return role, true
	}
	return resolvedRole{}, false
}

func assignmentSource(key assignmentKey, branched bool) string {
	switch {
	case branched:
		return "assigned to " + key.target + ", which this session was branched from"
	case key.kind == registry.AssignSession:
		return "assigned to this session"
	}
	return "assigned to " + key.kind + " @" + key.target
}

// sessionRoles is the role a session on this host acts under itself and,
// when its agent identity may be reached from elsewhere, the role a caller
// reaching it that way acts under; the two differ in their implicit role.
func (d *Daemon) sessionRoles(sessionID string) (local, remote *mcpRoleView) {
	record, err := d.Registry.Caller(sessionID)
	if err != nil {
		return nil, nil
	}
	role := d.callerRole(record, false)
	local = roleView(&role)
	if record.Identity == "" {
		return local, nil
	}
	if _, err := d.Registry.RemoteIdentityNamed(record.Identity); err != nil {
		return local, nil
	}
	reached := d.callerRole(record, true)
	return local, roleView(&reached)
}

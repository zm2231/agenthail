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

func (d *Daemon) callerRole(roles registry.SessionRoles, remote bool) resolvedRole {
	name, source := implicitRole(roles, remote)
	all, err := d.agentRoles()
	if err != nil {
		d.log.Printf("read agent roles: %s", err)
	}
	role := all[name]
	role.Source = source
	return role
}

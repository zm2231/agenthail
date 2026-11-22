package daemon

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/registry"
)

var roleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

func (r resolvedRole) view() daemonapi.AgentRoleView {
	view := daemonapi.AgentRoleView{Name: r.Name, BuiltIn: r.BuiltIn, Edited: r.Edited, Description: r.Description, Reach: r.Reach, Decisions: map[string]string{}, Definition: r.Role.Normalized(), AllTools: r.Tools == nil, Depth: r.DepthLimit(), Running: r.RunningLimit()}
	for _, action := range agentroles.Actions {
		view.Decisions[action.Name] = r.Decision(action.Name)
	}
	for _, tool := range agenttools.Tools {
		if r.Sees(tool.Name) {
			view.Tools = append(view.Tools, tool.Name)
		}
	}
	return view
}

func (d *Daemon) agentRolesView() (daemonapi.AgentRolesView, error) {
	roles, err := d.agentRoles()
	if err != nil {
		return daemonapi.AgentRolesView{}, err
	}
	view := daemonapi.AgentRolesView{Roles: make([]daemonapi.AgentRoleView, 0, len(roles))}
	for _, role := range roles {
		view.Roles = append(view.Roles, role.view())
	}
	sort.Slice(view.Roles, func(i, j int) bool {
		a, b := view.Roles[i], view.Roles[j]
		if a.BuiltIn != b.BuiltIn {
			return a.BuiltIn
		}
		return a.Name < b.Name
	})
	if view.Assignments, err = d.agentRoleAssignmentsView(roles); err != nil {
		return daemonapi.AgentRolesView{}, err
	}
	for _, action := range agentroles.Actions {
		view.Actions = append(view.Actions, daemonapi.AgentActionView(action))
	}
	for _, tool := range agenttools.Tools {
		view.Tools = append(view.Tools, tool.Name)
	}
	return view, nil
}

// agentRolesHandler serves the dashboard API, where a write is taken only from
// a paired device or from a paired host acting for the person. The local
// dashboard token proves nothing about who holds it, so the person's own
// computer changes roles over the local socket.
func (d *Daemon) agentRolesHandler(w http.ResponseWriter, r *http.Request) {
	p := principalFromRequest(r)
	if r.Method == http.MethodPost && p.Kind != principalDevice && (p.Kind != principalHost || !p.forThePerson()) {
		writeAPIError(w, http.StatusForbidden, "person_only", "Agent roles change from agenthail roles in the person's own terminal or from a paired device.")
		return
	}
	d.serveAgentRoles(w, r, p.actor())
}

func (d *Daemon) serveAgentRoles(w http.ResponseWriter, r *http.Request, actor string) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var request daemonapi.AgentRoleRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "send an op, a name and, for put, a role")
			return
		}
		if err := d.changeAgentRole(r.Context(), actor, request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
	default:
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		return
	}
	view, err := d.agentRolesView()
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	writeDashboardJSON(w, http.StatusOK, view)
}

// changeAgentRole writes the person's change and tells them who made it, so
// a change that did not come from them is seen.
func (d *Daemon) changeAgentRole(ctx context.Context, actor string, request daemonapi.AgentRoleRequest) error {
	name := strings.TrimSpace(request.Name)
	_, builtIn := agentroles.BuiltIn[name]
	switch request.Op {
	case "put":
		if !builtIn && !roleNamePattern.MatchString(name) {
			return fmt.Errorf("a role's name is lowercase letters, digits and dashes, starting with a letter, at most 40 long")
		}
		if err := request.Role.Validate(); err != nil {
			return err
		}
		definition, err := json.Marshal(request.Role.Normalized())
		if err != nil {
			return err
		}
		if _, err := d.Registry.PutAgentRole(name, definition, false); err != nil {
			return err
		}
		d.tellPerson(ctx, "Agent role changed", fmt.Sprintf("%s changed the %s role.", actor, name), "", "agent.role.changed")
	case "delete":
		roles, err := d.agentRoles()
		if err != nil {
			return err
		}
		if role, found := roles[name]; !found || (role.BuiltIn && !role.Edited) {
			return fmt.Errorf("no role %q to delete or reset", name)
		}
		if _, err := d.Registry.PutAgentRole(name, nil, true); err != nil {
			return err
		}
		what := "deleted"
		if roles[name].BuiltIn {
			what = "reset"
		}
		d.tellPerson(ctx, "Agent role changed", fmt.Sprintf("%s %s the %s role.", actor, what, name), "", "agent.role.changed")
	case "assign", "unassign":
		return d.changeAgentRoleAssignment(ctx, actor, request)
	default:
		return fmt.Errorf("op is put, delete, assign or unassign")
	}
	d.agentRolesChanged()
	return nil
}

func (d *Daemon) agentRolesChanged() {
	d.flushCatalog("agent roles")
	d.publishEvent("settings.updated", "", map[string]string{"source": "agent-roles"})
}

func (d *Daemon) adoptAgentRoles(records []registry.AgentRoleRecord) error {
	changed, err := d.Registry.AdoptAgentRoles(records)
	if err != nil || !changed {
		return err
	}
	d.agentRolesChanged()
	return nil
}

func (d *Daemon) agentRoleAssignmentsView(roles map[string]resolvedRole) ([]daemonapi.AgentRoleAssignmentView, error) {
	records, err := d.Registry.AgentRoleAssignments()
	if err != nil {
		return nil, err
	}
	views := []daemonapi.AgentRoleAssignmentView{}
	for _, record := range records {
		if record.Deleted {
			continue
		}
		view := daemonapi.AgentRoleAssignmentView{Kind: record.Kind, Target: record.Target, Role: record.Role}
		if _, exists := roles[record.Role]; !exists {
			view.Stale = "no role " + record.Role
		} else if record.Kind == registry.AssignSession {
			switch caller, err := d.Registry.Caller(record.Target); {
			case errors.Is(err, sql.ErrNoRows):
				view.Stale = "no such session"
			case err != nil:
				return nil, err
			case caller.Archived:
				view.Stale = "the session is archived"
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func (d *Daemon) changeAgentRoleAssignment(ctx context.Context, actor string, request daemonapi.AgentRoleRequest) error {
	kind, target := request.Kind, strings.TrimPrefix(strings.TrimSpace(request.Target), "@")
	if target == "" {
		return fmt.Errorf("name the session, identity or alias to %s", request.Op)
	}
	switch kind {
	case registry.AssignSession:
		id, err := d.Registry.ResolveTarget(strings.TrimSpace(request.Target))
		if err != nil {
			return fmt.Errorf("no session %s: %w", request.Target, err)
		}
		target = id
	case registry.AssignIdentity:
		if err := registry.ValidAgentIdentityName(target); err != nil {
			return err
		}
	case registry.AssignAlias:
	default:
		return fmt.Errorf("kind is session, identity or alias")
	}
	described := kind + " " + target
	if kind != registry.AssignSession {
		described = kind + " @" + target
	}
	if request.Op == "unassign" {
		records, err := d.Registry.AgentRoleAssignments()
		if err != nil {
			return err
		}
		if !slices.ContainsFunc(records, func(r registry.AgentRoleAssignment) bool {
			return r.Kind == kind && r.Target == target && !r.Deleted
		}) {
			return fmt.Errorf("%s has no role assigned", described)
		}
		if _, err := d.Registry.PutAgentRoleAssignment(kind, target, "", true); err != nil {
			return err
		}
		d.tellPerson(ctx, "Agent role changed", fmt.Sprintf("%s took the assigned role away from %s.", actor, described), "", "agent.role.changed")
		d.agentRolesChanged()
		return nil
	}
	name := strings.TrimSpace(request.Name)
	roles, err := d.agentRoles()
	if err != nil {
		return err
	}
	if _, exists := roles[name]; !exists {
		return fmt.Errorf("no role %q", name)
	}
	if _, err := d.Registry.PutAgentRoleAssignment(kind, target, name, false); err != nil {
		return err
	}
	d.tellPerson(ctx, "Agent role changed", fmt.Sprintf("%s gave %s the %s role.", actor, described, name), "", "agent.role.changed")
	d.agentRolesChanged()
	return nil
}

func (d *Daemon) adoptAgentRoleAssignments(records []registry.AgentRoleAssignment) error {
	changed, err := d.Registry.AdoptAgentRoleAssignments(records)
	if err != nil || !changed {
		return err
	}
	d.agentRolesChanged()
	return nil
}

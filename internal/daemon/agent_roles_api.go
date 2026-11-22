package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"

	"github.com/zm2231/agenthail/internal/agenttools"
	"github.com/zm2231/agenthail/internal/registry"
)

var roleNamePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

type AgentRoleView struct {
	Name        string            `json:"name"`
	BuiltIn     bool              `json:"builtIn"`
	Edited      bool              `json:"edited"`
	Description string            `json:"description"`
	Reach       string            `json:"reach"`
	Decisions   map[string]string `json:"decisions"`
	// Definition is the role as stored, which a change edits and puts back.
	Definition agentroles.Role `json:"definition"`
	Tools      []string        `json:"tools"`
	AllTools   bool            `json:"allTools"`
	Depth      int             `json:"depth"`
	Running    int             `json:"running"`
}

type AgentActionView struct {
	Name       string `json:"name"`
	Default    string `json:"default"`
	Detail     string `json:"detail"`
	OwnProject bool   `json:"ownProject"`
}

type AgentRolesView struct {
	Roles   []AgentRoleView   `json:"roles"`
	Actions []AgentActionView `json:"actions"`
	Tools   []string          `json:"tools"`
}

func (r resolvedRole) view() AgentRoleView {
	view := AgentRoleView{Name: r.Name, BuiltIn: r.BuiltIn, Edited: r.Edited, Description: r.Description, Reach: r.Reach, Decisions: map[string]string{}, Definition: r.Role.Normalized(), AllTools: r.Tools == nil, Depth: r.DepthLimit(), Running: r.RunningLimit()}
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

func (d *Daemon) agentRolesView() (AgentRolesView, error) {
	roles, err := d.agentRoles()
	if err != nil {
		return AgentRolesView{}, err
	}
	view := AgentRolesView{Roles: make([]AgentRoleView, 0, len(roles))}
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
	for _, action := range agentroles.Actions {
		view.Actions = append(view.Actions, AgentActionView(action))
	}
	for _, tool := range agenttools.Tools {
		view.Tools = append(view.Tools, tool.Name)
	}
	return view, nil
}

type AgentRoleRequest struct {
	// Op is put, which writes the whole role, or delete, which removes a role
	// the person made or returns a built-in one to how Agenthail ships it.
	Op   string          `json:"op"`
	Name string          `json:"name"`
	Role agentroles.Role `json:"role"`
}

func (d *Daemon) agentRolesHandler(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
	case http.MethodPost:
		var request AgentRoleRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&request); err != nil {
			writeAPIError(w, http.StatusBadRequest, "invalid_request", "send an op, a name and, for put, a role")
			return
		}
		if err := d.changeAgentRole(r.Context(), principalFromRequest(r).actor(), request); err != nil {
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
func (d *Daemon) changeAgentRole(ctx context.Context, actor string, request AgentRoleRequest) error {
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
	default:
		return fmt.Errorf("op is put or delete")
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

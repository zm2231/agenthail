package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/broadcast"

	"github.com/zm2231/agenthail/internal/daemonapi"
)

const rolesUsage = `usage: agenthail roles [list] [--json] | show <role> [--json] | explain <@alias|session-id|identity> [--json]
       agenthail roles create <name> [<from-role>] | delete <role> | reset <role>
       agenthail roles set <role> <action> allow|notify|ask|default
       agenthail roles reach <role> descendants|project|all
       agenthail roles limit <role> depth|running <n>
       agenthail roles tools <role> default | all | +<tool> -<tool> ...
       agenthail roles ledgers <role> all | own | default | +<project> -<project> ...
       agenthail roles assign <role> --session <@alias|id> | --identity <name> | --alias <name>
       agenthail roles unassign --session <@alias|id> | --identity <name> | --alias <name>`

const rolesTimeout = 10 * time.Second

func (a *App) cmdRoles(args []string) error {
	positional := stripFlags(args)
	asJSON := hasFlag(args, "--json")
	if len(positional) == 0 || positional[0] == "list" && len(positional) == 1 {
		return a.listRoles(asJSON)
	}
	if positional[0] == "show" && len(positional) == 2 {
		return a.showRole(positional[1], asJSON)
	}
	if positional[0] == "explain" && len(positional) == 2 {
		return a.explainRole(positional[1], asJSON)
	}
	if (positional[0] == "assign" || positional[0] == "unassign") && len(positional) <= 2 {
		return a.assignRole(positional[0], positional[1:], args)
	}
	view, err := a.fetchRoles()
	if err != nil {
		return err
	}
	verb, rest := positional[0], positional[1:]
	if verb == "create" && (len(rest) == 1 || len(rest) == 2) {
		from := "worker"
		if len(rest) == 2 {
			from = rest[1]
		}
		if _, found := roleNamed(view, rest[0]); found {
			return fmt.Errorf("a role named %s already exists", rest[0])
		}
		source, found := roleNamed(view, from)
		if !found {
			return fmt.Errorf("no role named %s to copy", from)
		}
		role := source.Definition
		role.Description = ""
		return a.putRole(rest[0], role)
	}
	if (verb == "delete" || verb == "reset") && len(rest) == 1 {
		role, found := roleNamed(view, rest[0])
		switch {
		case !found:
			return fmt.Errorf("no role named %s", rest[0])
		case verb == "delete" && role.BuiltIn:
			return fmt.Errorf("%s is built in; 'agenthail roles reset %s' returns it to how Agenthail ships it", role.Name, role.Name)
		case verb == "reset" && !role.BuiltIn:
			return fmt.Errorf("%s is a role you made; 'agenthail roles delete %s' removes it", role.Name, role.Name)
		}
		return a.postRole(daemonapi.AgentRoleRequest{Op: "delete", Name: role.Name})
	}
	if len(rest) < 2 {
		return errors.New(rolesUsage)
	}
	current, found := roleNamed(view, rest[0])
	if !found {
		return fmt.Errorf("no role named %s", rest[0])
	}
	if verb == "ledgers" {
		role, err := editLedgers(current.Definition, rest[1:], a.projectID)
		if err != nil {
			return err
		}
		return a.putRole(current.Name, role)
	}
	role, err := editRole(current.Definition, verb, rest[1:], view.Tools, current.Tools)
	if err != nil {
		return err
	}
	return a.putRole(current.Name, role)
}

// editRole changes a tool list from the tools the role sees now, so taking
// one away does not show the ones its decisions hide.
func editRole(role agentroles.Role, verb string, args []string, tools, seen []string) (agentroles.Role, error) {
	switch {
	case verb == "set" && len(args) == 2:
		actions := map[string]string{}
		for name, decision := range role.Actions {
			actions[name] = decision
		}
		if args[1] == "default" {
			delete(actions, args[0])
		} else {
			actions[args[0]] = args[1]
		}
		role.Actions = actions
	case verb == "reach" && len(args) == 1:
		role.Reach = args[0]
	case verb == "limit" && len(args) == 2:
		value, err := strconv.Atoi(args[1])
		if err != nil {
			return role, fmt.Errorf("a limit is a whole number, not %q", args[1])
		}
		switch args[0] {
		case "depth":
			role.Depth = value
		case "running":
			role.Running = value
		default:
			return role, errors.New("the limits are depth and running")
		}
	case verb == "tools" && len(args) == 1 && args[0] == "default":
		role.Tools = nil
	case verb == "tools" && len(args) == 1 && args[0] == "all":
		role.Tools = slices.Clone(tools)
	case verb == "tools":
		seen := slices.Clone(seen)
		for _, change := range args {
			name := strings.TrimLeft(change, "+-")
			if name == "" {
				return role, fmt.Errorf("a tool change is +<tool> or -<tool>, not %q", change)
			}
			switch change[0] {
			case '+':
				if !slices.Contains(seen, name) {
					seen = append(seen, name)
				}
			case '-':
				seen = slices.DeleteFunc(seen, func(tool string) bool { return tool == name })
			default:
				return role, fmt.Errorf("a tool change is +<tool> or -<tool>, not %q", change)
			}
		}
		role.Tools = seen
		if role.Tools == nil {
			role.Tools = []string{}
		}
	default:
		return role, errors.New(rolesUsage)
	}
	return role, nil
}

func (a *App) projectID(name string) (string, error) {
	project, err := broadcast.FindProject(a.registry(), name)
	return project.ID, err
}

// editLedgers drops a project that no longer resolves by the id it was
// stored under, so a removed project can still be taken off the list.
func editLedgers(role agentroles.Role, args []string, projectID func(string) (string, error)) (agentroles.Role, error) {
	if len(args) == 1 {
		switch args[0] {
		case agentroles.LedgersAll:
			role.Ledgers = []string{agentroles.LedgersAll}
			return role, nil
		case "own":
			role.Ledgers = []string{}
			return role, nil
		case "default":
			role.Ledgers = nil
			return role, nil
		}
	}
	if role.AllLedgers() {
		return role, errors.New("the role reaches every project's ledger; set 'own' first, then add projects")
	}
	ledgers := slices.Clone(role.Ledgers)
	if ledgers == nil {
		ledgers = []string{}
	}
	for _, change := range args {
		name := strings.TrimLeft(change, "+-")
		if name == "" || change[0] != '+' && change[0] != '-' {
			return role, fmt.Errorf("a ledger change is +<project> or -<project>, not %q", change)
		}
		id, err := projectID(name)
		if err != nil && (change[0] == '+' || !slices.Contains(ledgers, name)) {
			return role, err
		}
		if err != nil {
			id = name
		}
		if change[0] == '+' {
			if !slices.Contains(ledgers, id) {
				ledgers = append(ledgers, id)
			}
			continue
		}
		ledgers = slices.DeleteFunc(ledgers, func(project string) bool { return project == id })
	}
	role.Ledgers = ledgers
	return role, nil
}

func roleNamed(view daemonapi.AgentRolesView, name string) (daemonapi.AgentRoleView, bool) {
	for _, role := range view.Roles {
		if role.Name == name {
			return role, true
		}
	}
	return daemonapi.AgentRoleView{}, false
}

func (a *App) fetchRoles() (daemonapi.AgentRolesView, error) {
	client, err := a.localDaemonAPI()
	if err != nil {
		return daemonapi.AgentRolesView{}, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rolesTimeout)
	defer cancel()
	var view daemonapi.AgentRolesView
	err = client.getJSON(ctx, "/api/v1/agents", nil, &view)
	return view, err
}

func (a *App) putRole(name string, role agentroles.Role) error {
	if err := a.postRole(daemonapi.AgentRoleRequest{Op: "put", Name: name, Role: role}); err != nil {
		return err
	}
	return a.showRole(name, false)
}

func (a *App) postRole(request daemonapi.AgentRoleRequest) error {
	client, err := a.localDaemonAPI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rolesTimeout)
	defer cancel()
	_, _, err = client.call(ctx, http.MethodPost, "/api/v1/agents", nil, request, nil)
	return err
}

// assignRole reads "assign <role> --session|--identity|--alias <target>" or
// "unassign --session|--identity|--alias <target>".
func (a *App) assignRole(verb string, rest, args []string) error {
	var kind, target string
	for _, candidate := range []string{"session", "identity", "alias"} {
		if value := flagVal(args, "--"+candidate); value != "" {
			if kind != "" {
				return errors.New("name one of --session, --identity or --alias")
			}
			kind, target = candidate, value
		}
	}
	if kind == "" || (verb == "assign") != (len(rest) == 1) {
		return errors.New(rolesUsage)
	}
	request := daemonapi.AgentRoleRequest{Op: verb, Kind: kind, Target: target}
	if verb == "assign" {
		request.Name = rest[0]
	}
	if err := a.postRole(request); err != nil {
		return err
	}
	return a.listRoles(false)
}

func (a *App) listRoles(asJSON bool) error {
	view, err := a.fetchRoles()
	if err != nil {
		return err
	}
	if asJSON {
		return emit(view)
	}
	out := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(out, "ROLE\tREACH\tKIND\tDESCRIPTION")
	for _, role := range view.Roles {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", role.Name, role.Reach, roleKind(role), role.Description)
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if len(view.Assignments) == 0 {
		return nil
	}
	fmt.Println()
	out = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(out, "ASSIGNED TO\tROLE\tSTALE")
	for _, assignment := range view.Assignments {
		target := assignment.Kind + " " + assignment.Target
		if assignment.Kind != "session" {
			target = assignment.Kind + " @" + assignment.Target
		}
		fmt.Fprintf(out, "%s\t%s\t%s\n", target, assignment.Role, assignment.Stale)
	}
	return out.Flush()
}

func ledgerReach(role daemonapi.AgentRoleView) string {
	if role.AllLedgers {
		return "every project's"
	}
	if len(role.Ledgers) == 0 {
		return "its own project's"
	}
	return "its own project's and " + strings.Join(role.Ledgers, ", ")
}

func roleKind(role daemonapi.AgentRoleView) string {
	switch {
	case role.BuiltIn && role.Edited:
		return "built in, changed"
	case role.BuiltIn:
		return "built in"
	}
	return "yours"
}

func (a *App) showRole(name string, asJSON bool) error {
	view, err := a.fetchRoles()
	if err != nil {
		return err
	}
	role, found := roleNamed(view, name)
	if !found {
		return fmt.Errorf("no role named %s", name)
	}
	if asJSON {
		return emit(role)
	}
	fmt.Printf("%s (%s): %s\n", role.Name, roleKind(role), role.Description)
	return printRole(view.Actions, role)
}

func (a *App) explainRole(target string, asJSON bool) error {
	client, err := a.localDaemonAPI()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), rolesTimeout)
	defer cancel()
	var explained daemonapi.AgentRoleExplanation
	if err := client.getJSON(ctx, "/api/v1/agents", url.Values{"explain": {target}}, &explained); err != nil {
		return err
	}
	if asJSON {
		return emit(explained)
	}
	view, err := a.fetchRoles()
	if err != nil {
		return err
	}
	fmt.Printf("%s acts as %s (%s): %s\n", explained.Target, explained.Role.Name, roleKind(explained.Role), explained.Source)
	if explained.Replaces != "" {
		fmt.Printf("without that it would act as %s\n", explained.Replaces)
	}
	return printRole(view.Actions, explained.Role)
}

func printRole(actions []daemonapi.AgentActionView, role daemonapi.AgentRoleView) error {
	fmt.Printf("reach: %s\n", role.Reach)
	out := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(out, "ACTION\tDECISION\tSOURCE\tWHAT")
	for _, action := range actions {
		source := "default for reach " + role.Reach
		if _, changed := role.Definition.Actions[action.Name]; changed {
			source = "changed"
		}
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", action.Name, role.Decisions[action.Name], source, action.Detail)
	}
	if err := out.Flush(); err != nil {
		return err
	}
	if role.AllTools {
		fmt.Printf("tools (default, every one it may use): %s\n", strings.Join(role.Tools, ", "))
	} else {
		fmt.Printf("tools: %s\n", strings.Join(role.Tools, ", "))
	}
	fmt.Println("ledgers: " + ledgerReach(role))
	fmt.Printf("sessions it starts nest at most %d deep; it runs at most %d at once\n", role.Depth, role.Running)
	return nil
}

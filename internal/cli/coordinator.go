package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const coordinatorUsage = `usage:
  agenthail coordinator [--cwd <path>] [--json]
  agenthail coordinator list [--json]
  agenthail coordinator set <target> [--name <name>] [--instructions <file>] [--permission-mode <mode>] [--replace]
  agenthail coordinator new <claude|codex> [--cwd <path>] [--host <host>] [--name <name>] [--model <model>] [--instructions <file>] [--permission-mode <mode>] [--replace]
  agenthail coordinator clear [--cwd <path>]`

func (a *App) coordinator() coordinator.Service {
	return coordinator.Service{Registry: a.registry(), Adapters: a.adapters(), Dispatcher: delivery.Dispatcher{Registry: a.registry()}}
}

func (a *App) cmdCoordinator(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("registry not available")
	}
	positional := stripFlags(args)
	if len(positional) == 0 {
		return a.coordinatorShow(flagVal(args, "--cwd"), hasFlag(args, "--json"))
	}
	switch positional[0] {
	case "list":
		if len(positional) != 1 {
			return errors.New(coordinatorUsage)
		}
		return a.coordinatorList(hasFlag(args, "--json"))
	case "set":
		if len(positional) != 2 {
			return errors.New(coordinatorUsage)
		}
		return a.coordinatorSet(positional[1], args)
	case "new":
		if len(positional) != 2 {
			return errors.New(coordinatorUsage)
		}
		return a.coordinatorNew(positional[1], args)
	case "clear":
		if len(positional) != 1 {
			return errors.New(coordinatorUsage)
		}
		return a.coordinatorClear(flagVal(args, "--cwd"), hasFlag(args, "--json"))
	}
	return errors.New(coordinatorUsage)
}

// projectAt resolves the project containing a folder the user typed, the
// working directory when path is empty.
func (a *App) projectAt(path string) (*daemon.CatalogProject, error) {
	cwd, err := resolveThreadCwd(path)
	if err != nil {
		return nil, err
	}
	return a.projectOf(cwd)
}

// projectOf resolves the project of a session's folder, which may no longer
// exist: a removed worktree stays in its repository's project. A coordinator
// belongs to a project, so a folder outside every project has none.
func (a *App) projectOf(cwd string) (*daemon.CatalogProject, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	found, err := daemon.ResolvePathProject(ctx, a.registry(), cwd)
	if err != nil {
		return nil, fmt.Errorf("find the project of %s: %w", cwd, err)
	}
	if found == nil {
		return nil, fmt.Errorf("%s is not in a project; use a Git repository or run 'agenthail project add <folder>'", cwd)
	}
	return found, nil
}

// leadTarget turns @lead into the coordinator of the caller's project, and
// an @name no session here holds into the paired host's coordinator of that
// name. It returns the target to use, and the coordinator when a paired host
// owns it.
func (a *App) leadTarget(target string) (string, *registry.CoordinatorSpec, error) {
	name, ok := strings.CutPrefix(target, "@")
	if !ok || strings.Contains(name, "/") || a.registry() == nil {
		return target, nil, nil
	}
	if strings.EqualFold(name, registry.LeadAlias) {
		spec, err := a.callerLead()
		if err != nil {
			return "", nil, fmt.Errorf("@%s: %w", registry.LeadAlias, err)
		}
		if spec.HostID == "" {
			return "@" + spec.Name, nil, nil
		}
		return target, &spec, nil
	}
	if _, err := a.registry().ExactTarget("", name); err == nil {
		return target, nil, nil
	}
	if _, err := a.registry().CoordinatorNamed(name); err == nil {
		return target, nil, nil
	}
	named, err := a.registry().CoordinatorsNamed(name)
	if err != nil {
		return "", nil, err
	}
	switch len(named) {
	case 0:
		return target, nil, nil
	case 1:
		return target, &named[0], nil
	}
	return "", nil, fmt.Errorf("@%s names coordinators on several hosts: %s; send to one as <host>::@%s", name, a.coordinatorRefs(named), name)
}

// expandLead resolves @lead and a paired host's @name to a target the
// session resolvers take: a paired host's coordinator by its conversation.
func (a *App) expandLead(target string) (string, error) {
	resolved, remote, err := a.leadTarget(target)
	if err != nil || remote == nil {
		return resolved, err
	}
	if remote.SessionID == "" {
		return "", fmt.Errorf("%s has no conversation yet; send to it first, which starts one", a.coordinatorRef(*remote))
	}
	_, raw, _ := hosts.Split(remote.SessionID)
	return a.hostName(remote.HostID) + hosts.Separator + raw, nil
}

// callerLead is the coordinator of the caller's project: the calling
// session's folder, else the working directory. Two hosts' coordinators of
// one project are a conflict to resolve, never a pick.
func (a *App) callerLead() (registry.CoordinatorSpec, error) {
	var found *daemon.CatalogProject
	var err error
	if session := a.callerSession(); session != nil && session.Cwd != "" {
		found, err = a.projectOf(session.Cwd)
	} else {
		found, err = a.projectAt("")
	}
	if err != nil {
		return registry.CoordinatorSpec{}, err
	}
	specs, err := a.registry().ProjectCoordinators(found.ID)
	if err != nil {
		return registry.CoordinatorSpec{}, err
	}
	switch len(specs) {
	case 0:
		return registry.CoordinatorSpec{}, fmt.Errorf("%s has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'", found.RootPath)
	case 1:
		return specs[0], nil
	}
	return registry.CoordinatorSpec{}, fmt.Errorf("%s has coordinators %s; resolve it with 'agenthail coordinator set --replace' on one host", found.RootPath, a.coordinatorRefs(specs))
}

func (a *App) callerSession() *surface.Session {
	for _, variable := range callerEnv {
		id := os.Getenv(variable.name)
		if id == "" {
			continue
		}
		if sid, err := a.registry().ResolveTarget(id); err == nil {
			if session, err := a.registry().Session(sid); err == nil {
				return session
			}
		}
		break
	}
	return nil
}

func (a *App) hostName(hostID string) string {
	if host, err := a.registry().Host(hostID); err == nil && host.Name != "" {
		return host.Name
	}
	return hostID
}

// coordinatorRef is how a person names a coordinator: @name on this host,
// host::@name on a paired host.
func (a *App) coordinatorRef(spec registry.CoordinatorSpec) string {
	if spec.HostID == "" {
		return "@" + spec.Name
	}
	return a.hostName(spec.HostID) + hosts.Separator + "@" + spec.Name
}

func (a *App) coordinatorRefs(specs []registry.CoordinatorSpec) string {
	refs := make([]string, 0, len(specs))
	for _, spec := range specs {
		refs = append(refs, a.coordinatorRef(spec))
	}
	return strings.Join(refs, " and ")
}

func (a *App) coordinatorShow(path string, jsonOut bool) error {
	found, err := a.projectAt(path)
	if err != nil {
		return err
	}
	specs, err := a.registry().ProjectCoordinators(found.ID)
	if err != nil {
		return err
	}
	if len(specs) == 0 {
		return fmt.Errorf("%s has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'", found.RootPath)
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"projectId": found.ID, "coordinators": specs})
	}
	if len(specs) > 1 {
		fmt.Printf("%s has %d coordinators; resolve it with 'agenthail coordinator set --replace' on one host\n", found.RootPath, len(specs))
	}
	for _, spec := range specs {
		fmt.Printf("coordinator %s of %s\n", a.coordinatorRef(spec), spec.ProjectRoot)
		if spec.SessionID == "" {
			fmt.Println("  session:      none yet; the next message starts one")
		} else {
			fmt.Printf("  session:      %s\n", spec.SessionID)
		}
		fmt.Printf("  agent:        %s\n", spec.Surface)
		if spec.Cwd != "" {
			fmt.Printf("  cwd:          %s\n", spec.Cwd)
		}
		if spec.Model != "" {
			fmt.Printf("  model:        %s\n", spec.Model)
		}
		if spec.PermissionMode != "" {
			fmt.Printf("  permissions:  %s\n", spec.PermissionMode)
		}
		if spec.Instructions != "" {
			fmt.Printf("  instructions: %d characters, given to each new conversation\n", len(spec.Instructions))
		}
		if spec.ChangedBy != "" {
			fmt.Printf("  set by:       %s, %s\n", spec.ChangedBy, spec.ChangedAt.Local().Format(time.DateTime))
		}
	}
	return nil
}

func (a *App) coordinatorList(jsonOut bool) error {
	specs, err := a.registry().Coordinators()
	if err != nil {
		return err
	}
	type row struct {
		registry.CoordinatorSpec
		Host        string `json:"host"`
		ProjectSeen bool   `json:"projectSeen"`
	}
	rows := make([]row, 0, len(specs))
	for _, spec := range specs {
		entry := row{CoordinatorSpec: spec, Host: a.hostName(spec.HostID), ProjectSeen: true}
		if spec.HostID == "" {
			entry.Host = "local"
			found, err := a.projectOf(spec.ProjectRoot)
			entry.ProjectSeen = err == nil && found.ID == spec.ProjectID
		}
		rows = append(rows, entry)
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"coordinators": rows})
	}
	if len(rows) == 0 {
		fmt.Println("(no coordinators; use: agenthail coordinator new <claude|codex>)")
		return nil
	}
	fmt.Printf("%-12s %-24s %-7s %-38s %s\n", "HOST", "NAME", "AGENT", "SESSION", "PROJECT")
	for _, r := range rows {
		session := r.SessionID
		if session == "" {
			session = "not started"
		}
		project := r.ProjectRoot
		if !r.ProjectSeen {
			project += " (no project here now; move it with 'coordinator set --replace')"
		}
		fmt.Printf("%-12s @%-23s %-7s %-38s %s\n", truncate(r.Host, 12), r.Name, r.Surface, session, project)
	}
	return nil
}

// coordinatorPayload carries what set and new share: the name, the
// instructions file's text, the permission mode, the replace flag, and the
// calling session, which the coordinator's history names.
func (a *App) coordinatorPayload(action string, args []string) (map[string]any, error) {
	payload := map[string]any{"action": action}
	if name := strings.TrimSpace(flagVal(args, "--name")); name != "" {
		payload["name"] = name
	}
	if path := flagVal(args, "--instructions"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read instructions: %w", err)
		}
		payload["instructions"] = string(data)
	}
	if mode := flagVal(args, "--permission-mode"); mode != "" {
		payload["permissionMode"] = mode
	}
	if hasFlag(args, "--replace") {
		payload["replace"] = true
	}
	if session := a.callerSession(); session != nil {
		payload["sourceSessionId"] = session.ID
	}
	return payload, nil
}

type coordinatorAnswer struct {
	Coordinator registry.CoordinatorSpec   `json:"coordinator"`
	Replaced    []registry.CoordinatorSpec `json:"replaced"`
	Cleared     registry.CoordinatorSpec   `json:"cleared"`
}

func coordinatorAction(ctx context.Context, client *daemonAPIClient, payload map[string]any) (coordinatorAnswer, error) {
	var answer coordinatorAnswer
	data, status, err := client.action(ctx, payload)
	if err != nil {
		return answer, err
	}
	if status != http.StatusOK {
		return answer, actionError(data)
	}
	return answer, json.Unmarshal(data, &answer)
}

func actionError(data []byte) error {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(data, &envelope) == nil && envelope.Error.Message != "" {
		return errors.New(envelope.Error.Message)
	}
	return errors.New(strings.TrimSpace(string(data)))
}

// coordinatorSet makes a conversation its project's coordinator. The host
// that runs the conversation records it, so the conversation can be on any
// paired host.
func (a *App) coordinatorSet(target string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.DefaultTimeout)
	defer cancel()
	session, _, remote, err := a.resolveAnyTarget(ctx, target)
	if err != nil {
		return err
	}
	payload, err := a.coordinatorPayload("coordinator-set", args)
	if err != nil {
		return err
	}
	if remote != nil {
		payload["sessionId"] = remote.ID
	} else {
		payload["sessionId"] = session.ID
	}
	return a.runCoordinatorChange(ctx, payload, hasFlag(args, "--json"))
}

// coordinatorNew records a coordinator whose first message starts its
// conversation, on this host or the one --host names.
func (a *App) coordinatorNew(agent string, args []string) error {
	ctx, cancel := context.WithTimeout(context.Background(), a.DefaultTimeout)
	defer cancel()
	payload, err := a.coordinatorPayload("coordinator-new", args)
	if err != nil {
		return err
	}
	payload["surface"] = agent
	if model := flagVal(args, "--model"); model != "" {
		payload["model"] = model
	}
	cwd := strings.TrimSpace(flagVal(args, "--cwd"))
	if host := strings.TrimSpace(flagVal(args, "--host")); host != "" {
		if !filepath.IsAbs(cwd) {
			return fmt.Errorf("--host needs --cwd with an absolute folder on that host")
		}
		payload["host"], payload["cwd"] = host, cwd
	} else if cwd != "" {
		if payload["cwd"], err = resolveThreadCwd(cwd); err != nil {
			return err
		}
	} else if payload["path"], err = resolveThreadCwd(""); err != nil {
		return err
	}
	return a.runCoordinatorChange(ctx, payload, hasFlag(args, "--json"))
}

func (a *App) runCoordinatorChange(ctx context.Context, payload map[string]any, jsonOut bool) error {
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	answer, err := coordinatorAction(ctx, client, payload)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(answer)
	}
	spec := answer.Coordinator
	for _, previous := range answer.Replaced {
		fmt.Printf("replaced %s\n", a.coordinatorRef(previous))
	}
	if spec.SessionID == "" {
		fmt.Printf("@%s coordinates %s; the first message to it starts its conversation in %s\n", spec.Name, spec.ProjectRoot, spec.Cwd)
		return nil
	}
	fmt.Printf("@%s coordinates %s (%s)\n", spec.Name, spec.ProjectRoot, spec.SessionID)
	return nil
}

// coordinatorClear clears the coordinator of the project at a folder, on
// whichever host holds it. Its conversation and name stay.
func (a *App) coordinatorClear(path string, jsonOut bool) error {
	found, err := a.projectAt(path)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), a.DefaultTimeout)
	defer cancel()
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	payload := map[string]any{"action": "coordinator-clear", "projectId": found.ID}
	if session := a.callerSession(); session != nil {
		payload["sourceSessionId"] = session.ID
	}
	answer, err := coordinatorAction(ctx, client, payload)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(answer)
	}
	fmt.Printf("cleared coordinator %s of %s; its session and name are unchanged\n", a.coordinatorRef(answer.Cleared), found.RootPath)
	return nil
}

// sendToPeerCoordinator delivers to a paired host's coordinator through
// that host, which starts its conversation when it has none.
func (a *App) sendToPeerCoordinator(ctx context.Context, spec registry.CoordinatorSpec, message string, args []string) error {
	if hasFlag(args, "--no-queue") {
		return fmt.Errorf("--no-queue is not available for %s: the daemon holds a send to another host until that host answers", a.coordinatorRef(spec))
	}
	client, err := a.daemonAPI()
	if err != nil {
		return err
	}
	payload := map[string]any{"action": "coordinator-send", "host": spec.HostID, "name": spec.Name, "message": message}
	source, err := a.sourceSessionID(ctx, flagVal(args, "--from"))
	if err != nil {
		return err
	}
	if source != "" {
		payload["sourceSessionId"] = source
	}
	if model := flagVal(args, "--model"); model != "" {
		payload["model"] = model
	}
	data, status, err := client.action(ctx, payload)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return actionError(data)
	}
	var answer struct {
		Result  delivery.Receipt `json:"result"`
		Started bool             `json:"started"`
	}
	if err := json.Unmarshal(data, &answer); err != nil {
		return err
	}
	if hasFlag(args, "--json") {
		return json.NewEncoder(os.Stdout).Encode(answer)
	}
	fmt.Println(answer.Result.Detail)
	if answer.Started {
		fmt.Printf("read its reply with 'agenthail last %s'\n", a.coordinatorRef(spec))
	}
	return nil
}

// startCoordinator delivers to a coordinator whose name does not point at a
// session yet, which starts its conversation from the saved spec.
func (a *App) startCoordinator(ctx context.Context, coord coordinator.Service, spec registry.CoordinatorSpec, message string, args []string) error {
	if hasFlag(args, "--stream") || hasFlag(args, "--reply") {
		return fmt.Errorf("the coordinator has no conversation yet; send without --stream or --reply, then read it with 'agenthail last'")
	}
	options := surface.SendOptions{Model: flagVal(args, "--model")}
	deliver := coord.Deliver
	if hasFlag(args, "--no-queue") {
		deliver = coord.DeliverWithoutQueue
	}
	outcome, err := deliver(ctx, spec, message, options)
	if err != nil {
		return err
	}
	if !outcome.Started {
		return printCoordinatorReceipt(outcome, hasFlag(args, "--json"))
	}
	return printCoordinatorStart(outcome, "@"+spec.Name, hasFlag(args, "--json"))
}

func printCoordinatorStart(outcome *coordinator.Outcome, target string, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(outcome)
	}
	fmt.Println(outcome.Receipt.Detail)
	fmt.Printf("read its reply with 'agenthail last %s'\n", target)
	return nil
}

func printCoordinatorReceipt(outcome *coordinator.Outcome, jsonOut bool) error {
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(outcome.Receipt)
	}
	fmt.Println(outcome.Receipt.Detail)
	return nil
}

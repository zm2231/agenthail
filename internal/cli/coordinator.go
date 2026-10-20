package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const coordinatorUsage = `usage:
  agenthail coordinator [--cwd <path>] [--json]
  agenthail coordinator list [--json]
  agenthail coordinator set <target> [--name <name>] [--instructions <file>] [--permission-mode <mode>]
  agenthail coordinator new <claude|codex> [--cwd <path>] [--name <name>] [--model <model>] [--instructions <file>] [--permission-mode <mode>]
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
		found, err := a.projectAt(flagVal(args, "--cwd"))
		if err != nil {
			return err
		}
		if err := a.registry().ClearCoordinator(found.ID); err != nil {
			return err
		}
		fmt.Printf("cleared the coordinator of %s; its session and name are unchanged\n", found.RootPath)
		return nil
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

// expandLead turns @lead into the name of the coordinator of the caller's
// project. The caller is the session running this command when the registry
// knows it, otherwise the working directory.
func (a *App) expandLead(target string) (string, error) {
	name, ok := strings.CutPrefix(target, "@")
	if !ok || !strings.EqualFold(name, registry.LeadAlias) || a.registry() == nil {
		return target, nil
	}
	cwd := a.callerCwd()
	var found *daemon.CatalogProject
	var err error
	if cwd == "" {
		found, err = a.projectAt("")
	} else {
		found, err = a.projectOf(cwd)
	}
	if err != nil {
		return "", fmt.Errorf("@%s: %w", registry.LeadAlias, err)
	}
	spec, err := a.registry().Coordinator(found.ID)
	if errors.Is(err, registry.ErrNoCoordinator) {
		return "", fmt.Errorf("@%s: %s has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'", registry.LeadAlias, found.RootPath)
	}
	if err != nil {
		return "", err
	}
	return "@" + spec.Name, nil
}

func (a *App) callerCwd() string {
	for _, variable := range callerEnv {
		id := os.Getenv(variable.name)
		if id == "" {
			continue
		}
		if sid, err := a.registry().ResolveTarget(id); err == nil {
			if session, err := a.registry().Session(sid); err == nil && session.Cwd != "" {
				return session.Cwd
			}
		}
		break
	}
	return ""
}

func (a *App) coordinatorShow(path string, jsonOut bool) error {
	found, err := a.projectAt(path)
	if err != nil {
		return err
	}
	spec, err := a.registry().Coordinator(found.ID)
	if errors.Is(err, registry.ErrNoCoordinator) {
		return fmt.Errorf("%s has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'", found.RootPath)
	}
	if err != nil {
		return err
	}
	current, err := a.registry().CoordinatorSessionID(spec)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"coordinator": spec, "sessionId": current})
	}
	fmt.Printf("coordinator @%s of %s\n", spec.Name, spec.ProjectRoot)
	if current == "" {
		fmt.Println("  session:      none yet; the next message starts one")
	} else {
		fmt.Printf("  session:      %s\n", current)
	}
	fmt.Printf("  agent:        %s\n  cwd:          %s\n", spec.Surface, spec.Cwd)
	if spec.Model != "" {
		fmt.Printf("  model:        %s\n", spec.Model)
	}
	if spec.PermissionMode != "" {
		fmt.Printf("  permissions:  %s\n", spec.PermissionMode)
	}
	if spec.Instructions != "" {
		fmt.Printf("  instructions: %d characters, given to each new conversation\n", len(spec.Instructions))
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
		SessionID string `json:"sessionId"`
	}
	rows := make([]row, 0, len(specs))
	for _, spec := range specs {
		current, err := a.registry().CoordinatorSessionID(spec)
		if err != nil {
			return err
		}
		rows = append(rows, row{CoordinatorSpec: spec, SessionID: current})
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{"coordinators": rows})
	}
	if len(rows) == 0 {
		fmt.Println("(no coordinators; use: agenthail coordinator new <claude|codex>)")
		return nil
	}
	for _, r := range rows {
		session := r.SessionID
		if session == "" {
			session = "not started"
		}
		fmt.Printf("@%-24s %-7s %-38s %s\n", r.Name, r.Surface, session, r.ProjectRoot)
	}
	return nil
}

func (a *App) coordinatorSet(target string, args []string) error {
	ctx := context.Background()
	session, _, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if session.Cwd == "" {
		return fmt.Errorf("%s has no known working directory to start again in", a.resolveDisplay(session.ID))
	}
	found, err := a.projectOf(session.Cwd)
	if err != nil {
		return err
	}
	spec, err := a.coordinatorSpec(args, found, string(session.Surface), session.Cwd, session.ConfiguredModel)
	if err != nil {
		return err
	}
	if err := a.registry().SetCoordinator(spec, session.ID); err != nil {
		return coordinatorNameError(err)
	}
	fmt.Printf("@%s coordinates %s (%s)\n", spec.Name, spec.ProjectRoot, session.ID)
	return nil
}

func (a *App) coordinatorNew(agent string, args []string) error {
	kind := surface.SurfaceKind(agent)
	if kind != surface.KindClaude && kind != surface.KindCodex {
		return fmt.Errorf("a coordinator is a claude or codex conversation, not %q", agent)
	}
	if _, ok := a.adapters().ForSurface(kind).(surface.SessionStarter); !ok {
		return fmt.Errorf("%s is not configured to start conversations here", agent)
	}
	found, err := a.projectAt(flagVal(args, "--cwd"))
	if err != nil {
		return err
	}
	cwd := found.RootPath
	if flagVal(args, "--cwd") != "" {
		if cwd, err = resolveThreadCwd(flagVal(args, "--cwd")); err != nil {
			return err
		}
	}
	spec, err := a.coordinatorSpec(args, found, agent, cwd, flagVal(args, "--model"))
	if err != nil {
		return err
	}
	if err := a.registry().EnsureAliasAvailable(spec.Name); err != nil {
		if previous, specErr := a.registry().Coordinator(found.ID); specErr != nil || previous.Name != spec.Name {
			return coordinatorNameError(err)
		}
	}
	if err := a.registry().SetCoordinator(spec, ""); err != nil {
		return coordinatorNameError(err)
	}
	fmt.Printf("@%s coordinates %s; the first message to it starts its conversation in %s\n", spec.Name, spec.ProjectRoot, cwd)
	return nil
}

func (a *App) coordinatorSpec(args []string, found *daemon.CatalogProject, agent, cwd, model string) (registry.CoordinatorSpec, error) {
	name := strings.TrimPrefix(strings.TrimSpace(flagVal(args, "--name")), "@")
	if name == "" {
		name = defaultCoordinatorName(found.DisplayName)
	}
	if name == "" || strings.ContainsAny(name, " \t\r\n/#:") || len(name) > 80 {
		return registry.CoordinatorSpec{}, fmt.Errorf("name must be 1 to 80 characters without spaces, /, :, or #")
	}
	spec := registry.CoordinatorSpec{ProjectID: found.ID, ProjectRoot: found.RootPath, Name: name, Surface: agent, Cwd: cwd, Model: model, PermissionMode: flagVal(args, "--permission-mode")}
	if spec.PermissionMode != "" && agent != string(surface.KindClaude) {
		return spec, fmt.Errorf("--permission-mode applies to a claude coordinator only")
	}
	if previous, err := a.registry().Coordinator(found.ID); err == nil {
		spec.Instructions = previous.Instructions
	}
	if path := flagVal(args, "--instructions"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return spec, fmt.Errorf("read instructions: %w", err)
		}
		spec.Instructions = string(data)
	}
	return spec, nil
}

var coordinatorNameCharacters = regexp.MustCompile(`[^a-z0-9._-]+`)

// defaultCoordinatorName is @<project>-lead, from the project's display name.
func defaultCoordinatorName(displayName string) string {
	const suffix = "-lead"
	base := strings.Trim(coordinatorNameCharacters.ReplaceAllString(strings.ToLower(displayName), "-"), "-.")
	if len(base) > 80-len(suffix) {
		base = strings.TrimRight(base[:80-len(suffix)], "-.")
	}
	if base == "" {
		base = "project"
	}
	return base + suffix
}

func coordinatorNameError(err error) error {
	var taken registry.AliasTakenError
	if errors.As(err, &taken) {
		return fmt.Errorf("@%s already names session %s; choose another --name", taken.Name, taken.Owner)
	}
	var coordinatorTaken registry.CoordinatorNameTakenError
	if errors.As(err, &coordinatorTaken) {
		return fmt.Errorf("%w; choose another --name", coordinatorTaken)
	}
	return err
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

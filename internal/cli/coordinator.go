package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/coordinator"
	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const coordinatorUsage = `usage:
  agenthail coordinator [--json]
  agenthail coordinator set <target> [--name <name>] [--instructions <file>] [--permission-mode <mode>]
  agenthail coordinator new <claude|codex> --cwd <path> [--name <name>] [--model <model>] [--instructions <file>] [--permission-mode <mode>]
  agenthail coordinator clear`

func (a *App) coordinator() coordinator.Service {
	return coordinator.Service{Registry: a.registry(), Adapters: a.adapters(), Dispatcher: delivery.Dispatcher{Registry: a.registry()}}
}

func (a *App) cmdCoordinator(args []string) error {
	if a.registry() == nil {
		return fmt.Errorf("registry not available")
	}
	positional := stripFlags(args)
	if len(positional) == 0 {
		return a.coordinatorShow(hasFlag(args, "--json"))
	}
	switch positional[0] {
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
		if err := a.registry().ClearCoordinator(); err != nil {
			return err
		}
		fmt.Println("cleared the coordinator; its session and name are unchanged")
		return nil
	}
	return errors.New(coordinatorUsage)
}

func (a *App) coordinatorShow(jsonOut bool) error {
	spec, err := a.registry().Coordinator()
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
	fmt.Printf("coordinator @%s\n", spec.Name)
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

func (a *App) coordinatorSet(target string, args []string) error {
	ctx := context.Background()
	session, _, err := a.resolveTarget(ctx, target)
	if err != nil {
		return err
	}
	if session.Cwd == "" {
		return fmt.Errorf("%s has no known working directory to start again in", a.resolveDisplay(session.ID))
	}
	name := coordinatorName(args)
	if name == "" {
		if alias, aliasErr := a.registry().ReverseAlias(session.ID); aliasErr == nil && alias != "" {
			name = alias
		} else {
			name = "coordinator"
		}
	}
	spec, err := a.coordinatorSpec(args, name, string(session.Surface), session.Cwd, session.ConfiguredModel)
	if err != nil {
		return err
	}
	if err := a.registry().SetCoordinator(spec, session.ID); err != nil {
		return coordinatorNameError(err)
	}
	fmt.Printf("@%s is the coordinator (%s)\n", spec.Name, session.ID)
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
	cwd, err := resolveThreadCwd(flagVal(args, "--cwd"))
	if err != nil {
		return err
	}
	name := coordinatorName(args)
	if name == "" {
		name = "coordinator"
	}
	if err := a.registry().EnsureAliasAvailable(name); err != nil {
		return coordinatorNameError(err)
	}
	spec, err := a.coordinatorSpec(args, name, agent, cwd, flagVal(args, "--model"))
	if err != nil {
		return err
	}
	if err := a.registry().SetCoordinator(spec, ""); err != nil {
		return err
	}
	fmt.Printf("@%s is the coordinator; the first message to it starts its conversation in %s\n", spec.Name, cwd)
	return nil
}

func (a *App) coordinatorSpec(args []string, name, agent, cwd, model string) (registry.CoordinatorSpec, error) {
	if strings.ContainsAny(name, " \t\r\n/#") || len(name) > 80 {
		return registry.CoordinatorSpec{}, fmt.Errorf("name must be 1 to 80 characters without spaces, /, or #")
	}
	spec := registry.CoordinatorSpec{Name: name, Surface: agent, Cwd: cwd, Model: model, PermissionMode: flagVal(args, "--permission-mode")}
	if spec.PermissionMode != "" && agent != string(surface.KindClaude) {
		return spec, fmt.Errorf("--permission-mode applies to a claude coordinator only")
	}
	if previous, err := a.registry().Coordinator(); err == nil {
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

func coordinatorName(args []string) string {
	return strings.TrimPrefix(strings.TrimSpace(flagVal(args, "--name")), "@")
}

func coordinatorNameError(err error) error {
	var taken registry.AliasTakenError
	if errors.As(err, &taken) {
		return fmt.Errorf("@%s already names session %s; choose another --name", taken.Name, taken.Owner)
	}
	return err
}

// startCoordinator delivers to a coordinator whose name no longer points at a
// session, which starts its conversation from the saved spec.
func (a *App) startCoordinator(ctx context.Context, coord coordinator.Service, message string, args []string) error {
	if hasFlag(args, "--stream") || hasFlag(args, "--reply") {
		return fmt.Errorf("the coordinator has no conversation yet; send without --stream or --reply, then read it with 'agenthail last'")
	}
	options := surface.SendOptions{Model: flagVal(args, "--model")}
	deliver := coord.Deliver
	if hasFlag(args, "--no-queue") {
		deliver = coord.DeliverWithoutQueue
	}
	outcome, err := deliver(ctx, message, options)
	if err != nil {
		return err
	}
	if !outcome.Started {
		return printCoordinatorReceipt(outcome, hasFlag(args, "--json"))
	}
	return printCoordinatorStart(outcome, "@"+outcome.Session.Name, hasFlag(args, "--json"))
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

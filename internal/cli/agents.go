package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/zm2231/agenthail/internal/agentswitch"
	"github.com/zm2231/agenthail/internal/daemonapi"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

const agentsUsage = `usage: agenthail agents [list] [--json]
       agenthail agents use <agent> auto|on|off

Each agent that runs on this computer is auto by default: Agenthail works with
it when it is installed, and a missing one is unavailable rather than a
failure. 'on' makes a missing one a failure; 'off' leaves the agent alone.`

func (a *App) cmdAgents(args []string) error {
	if hasFlag(args, "--help") {
		fmt.Println(agentsUsage)
		return nil
	}
	positional := stripFlags(args)
	switch {
	case len(positional) == 0 || len(positional) == 1 && (positional[0] == "list" || positional[0] == "ls"):
		return a.listAgents(hasFlag(args, "--json"))
	case len(positional) == 3 && positional[0] == "use":
		return a.useAgent(positional[1], positional[2])
	default:
		return fmt.Errorf("%s", agentsUsage)
	}
}

func (a *App) agentStatuses() ([]surface.AgentStatus, error) {
	config, err := dashconfig.LoadAgentModes()
	if err != nil {
		return nil, err
	}
	adapters := make([]surface.Surface, 0, len(a.Surfaces))
	for _, entry := range a.Surfaces {
		adapters = append(adapters, entry.Surface)
	}
	return surface.AgentStatuses(adapters, config.AgentMode), nil
}

func (a *App) listAgents(jsonOut bool) error {
	statuses, err := a.agentStatuses()
	if err != nil {
		return err
	}
	if jsonOut {
		return emit(map[string]any{"agents": statuses})
	}
	for _, status := range statuses {
		fmt.Printf("%-8s %-5s %s\n", status.ID, status.Mode, describeAgentState(status))
	}
	fmt.Println("  change: agenthail agents use <agent> auto|on|off, or Settings > Agents")
	return nil
}

func describeAgentState(status surface.AgentStatus) string {
	switch status.State {
	case surface.AgentOn:
		return "in use"
	case surface.AgentOff:
		return "off"
	case surface.AgentUnavailable:
		return "unavailable: not installed here"
	}
	return "missing: " + status.Detail
}

func (a *App) useAgent(agent, value string) error {
	mode, err := agentswitch.ParseMode(value)
	if err != nil {
		return err
	}
	ctx, cancel, err := a.deadlineContext(a.DefaultTimeout)
	if err != nil {
		return err
	}
	defer cancel()
	if err := a.personAction(ctx, daemonapi.PersonActionSettings, map[string]any{"action": "agent-mode", "agent": agent, "mode": string(mode)}); err != nil {
		return err
	}
	statuses, err := a.agentStatuses()
	if err != nil {
		return err
	}
	index := slices.IndexFunc(statuses, func(status surface.AgentStatus) bool { return status.ID == agent })
	if index < 0 {
		ids := make([]string, len(statuses))
		for i, status := range statuses {
			ids[i] = status.ID
		}
		return fmt.Errorf("unknown agent %q; agents: %s", agent, strings.Join(ids, ", "))
	}
	if _, err := dashconfig.Update(func(config *dashconfig.Config) error {
		config.SetAgentMode(agent, mode)
		return nil
	}); err != nil {
		return err
	}
	statuses, err = a.agentStatuses()
	if err != nil {
		return err
	}
	fmt.Printf("%s is %s: %s\n", statuses[index].Label, mode, describeAgentState(statuses[index]))
	return nil
}

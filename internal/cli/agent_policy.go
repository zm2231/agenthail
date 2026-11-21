package cli

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/zm2231/agenthail/internal/daemon"
)

const agentPolicyUsage = `usage: agenthail agent-policy [list] [--json] | set <action> allow|notify|ask | reset <action> | limit depth|running <n>`

func (a *App) cmdAgentPolicy(args []string) error {
	positional := stripFlags(args)
	if len(positional) == 0 || positional[0] == "list" && len(positional) == 1 {
		return a.listAgentPolicy(hasFlag(args, "--json"))
	}
	if a.agentEnvironment() {
		return errors.New("the agent policy is the person's to change; an agent may read it with 'agenthail agent-policy'")
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	switch {
	case positional[0] == "set" && len(positional) == 3:
		err = config.SetAgentDecision(positional[1], positional[2])
	case positional[0] == "reset" && len(positional) == 2:
		err = config.SetAgentDecision(positional[1], "")
	case positional[0] == "limit" && len(positional) == 3:
		value, convErr := strconv.Atoi(positional[2])
		if convErr != nil {
			return fmt.Errorf("a limit is a whole number, not %q", positional[2])
		}
		err = config.SetAgentLimit(positional[1], value)
	default:
		return errors.New(agentPolicyUsage)
	}
	if err != nil {
		return err
	}
	if err := daemon.SaveDashboardConfig(config); err != nil {
		return err
	}
	return a.listAgentPolicy(false)
}

func (a *App) listAgentPolicy(asJSON bool) error {
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	policy := config.Agents
	if asJSON {
		return emit(map[string]any{"actions": policy.Rows(), "depth": policy.DepthLimit(), "running": policy.RunningLimit()})
	}
	out := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(out, "ACTION\tDECISION\tDEFAULT\tWHAT")
	for _, row := range policy.Rows() {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\n", row.Action, row.Decision, row.Default, row.Detail)
	}
	if err := out.Flush(); err != nil {
		return err
	}
	fmt.Printf("sessions nest at most %d deep; each agent runs at most %d at once\n", policy.DepthLimit(), policy.RunningLimit())
	return nil
}

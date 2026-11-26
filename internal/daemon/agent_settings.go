package daemon

import (
	"fmt"
	"slices"

	"github.com/zm2231/agenthail/internal/agentswitch"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) agentGate(adapter surface.Surface) error {
	config, _ := dashconfig.Load()
	return surface.AgentGate(adapter, config.AgentMode(string(adapter.Name())))
}

func (d *Daemon) agentStatuses(config dashconfig.Config) []surface.AgentStatus {
	return surface.AgentStatuses(d.Surfaces, config.AgentMode)
}

func (d *Daemon) setAgentMode(config *dashconfig.Config, agent, value string) error {
	mode, err := agentswitch.ParseMode(value)
	if err != nil {
		return err
	}
	if !slices.ContainsFunc(d.agentStatuses(*config), func(status surface.AgentStatus) bool { return status.ID == agent }) {
		return fmt.Errorf("%q is not an agent this computer can turn on or off", agent)
	}
	config.SetAgentMode(agent, mode)
	if err := dashconfig.Save(*config); err != nil {
		return err
	}
	d.requestCatalogDiscovery()
	return nil
}

package daemon

import (
	"fmt"
	"slices"

	"github.com/zm2231/agenthail/internal/agentswitch"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) agentGate(adapter surface.Surface) error {
	if _, local := adapter.(surface.LocalAgent); !local {
		return nil
	}
	config, err := dashconfig.LoadAgentModes()
	if err != nil {
		return fmt.Errorf("whether %s is turned on or off cannot be read until %s is fixed: %w", adapter.Name(), dashconfig.Path(), err)
	}
	return surface.AgentTurnedOff(adapter, config.AgentMode(string(adapter.Name())))
}

func (d *Daemon) agentHealth(adapter surface.Surface, probeErr error) error {
	config, err := dashconfig.LoadAgentModes()
	if err != nil {
		return probeErr
	}
	return surface.AgentHealth(adapter, config.AgentMode(string(adapter.Name())), probeErr)
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

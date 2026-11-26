package surface

import (
	"errors"

	"github.com/zm2231/agenthail/internal/agentswitch"
)

// LocalAgent is an agent whose program runs on this computer, so the
// person's on/off setting decides whether a missing program is a failure.
type LocalAgent interface {
	AgentLabel() string
	LocateProgram() error
}

// RequiredAgent is a local agent the person set up themselves, such as an
// agent added in config; it fails when missing even on auto.
type RequiredAgent interface {
	RequiredOnAuto() bool
}

// AgentTurnedOff is the agentswitch.OffError of a local agent the person
// turned off, and nil for any other adapter.
func AgentTurnedOff(adapter Surface, mode agentswitch.Mode) error {
	if _, ok := adapter.(LocalAgent); !ok || mode != agentswitch.Off {
		return nil
	}
	return AgentHealth(adapter, mode, nil)
}

// AgentHealth reads probeErr, the adapter's own health or listing outcome,
// through the person's setting for it.
func AgentHealth(adapter Surface, mode agentswitch.Mode, probeErr error) error {
	local, ok := adapter.(LocalAgent)
	if !ok {
		return probeErr
	}
	if required, ok := adapter.(RequiredAgent); ok && mode == agentswitch.Auto && required.RequiredOnAuto() {
		mode = agentswitch.On
	}
	return agentswitch.Check(agentswitch.Agent{ID: string(adapter.Name()), Label: local.AgentLabel(), Locate: local.LocateProgram}, mode, probeErr)
}

type AgentState string

const (
	AgentOn          AgentState = "on"
	AgentOff         AgentState = "off"
	AgentUnavailable AgentState = "unavailable"
	AgentMissing     AgentState = "missing"
)

var errNotProbed = errors.New("not probed")

type AgentStatus struct {
	ID     string           `json:"id"`
	Label  string           `json:"label"`
	Mode   agentswitch.Mode `json:"mode"`
	State  AgentState       `json:"state"`
	Detail string           `json:"detail,omitempty"`
}

func AgentStatuses(adapters []Surface, mode func(agent string) agentswitch.Mode) []AgentStatus {
	var statuses []AgentStatus
	for _, adapter := range adapters {
		local, ok := adapter.(LocalAgent)
		if !ok {
			continue
		}
		id := string(adapter.Name())
		status := AgentStatus{ID: id, Label: local.AgentLabel(), Mode: mode(id), State: AgentOn}
		if err := AgentHealth(adapter, status.Mode, errNotProbed); err != nil && err != errNotProbed {
			status.Detail = err.Error()
			switch {
			case agentswitch.IsOff(err):
				status.State = AgentOff
			case agentswitch.IsUnavailable(err):
				status.State = AgentUnavailable
			default:
				status.State = AgentMissing
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

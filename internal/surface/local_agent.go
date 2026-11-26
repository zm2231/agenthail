package surface

import "github.com/zm2231/agenthail/internal/agentswitch"

// LocalAgent is an agent whose program runs on this computer, so the
// person's on/off setting decides whether a missing program is a failure.
type LocalAgent interface {
	AgentLabel() string
	LocateProgram() error
}

// AgentGate is nil for an adapter Agenthail works with here under mode, and
// otherwise the agentswitch error that says why not.
func AgentGate(adapter Surface, mode agentswitch.Mode) error {
	local, ok := adapter.(LocalAgent)
	if !ok {
		return nil
	}
	return agentswitch.Check(agentswitch.Agent{ID: string(adapter.Name()), Label: local.AgentLabel(), Locate: local.LocateProgram}, mode)
}

type AgentState string

const (
	AgentOn          AgentState = "on"
	AgentOff         AgentState = "off"
	AgentUnavailable AgentState = "unavailable"
	AgentMissing     AgentState = "missing"
)

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
		if err := AgentGate(adapter, status.Mode); err != nil {
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

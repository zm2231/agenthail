package daemon

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"strings"

	"github.com/zm2231/agenthail/internal/agentroles"
	"github.com/zm2231/agenthail/internal/dashconfig"
)

type foldedAgentPolicy struct {
	Actions map[string]string `json:"actions"`
	Depth   int               `json:"depth"`
	Running int               `json:"running"`
}

// foldAgentPolicy moves the choices a person made in dashboard.json's
// agents policy into the built-in roles they applied to, then drops it from
// the file. A lead's own-project choices only ever reached leads, and the
// rest reached every agent.
func (d *Daemon) foldAgentPolicy() error {
	data, err := os.ReadFile(dashconfig.Path())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var raw struct {
		Agents *foldedAgentPolicy `json:"agents"`
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw.Agents == nil {
		return err
	}
	roles, err := d.agentRoles()
	if err != nil {
		return err
	}
	var changed []string
	for _, name := range []string{agentroles.Worker, agentroles.Lead, agentroles.Operator, agentroles.Remote} {
		before, err := json.Marshal(roles[name].Role.Normalized())
		if err != nil {
			return err
		}
		role := roles[name].Role
		role.Actions = maps.Clone(role.Actions)
		if role.Actions == nil {
			role.Actions = map[string]string{}
		}
		for action, decision := range raw.Agents.Actions {
			row, known := agentroles.ActionNamed(action)
			if !known || !agentroles.ValidDecision(decision) || (row.OwnProject && name != agentroles.Lead) {
				continue
			}
			role.Actions[action] = decision
		}
		if raw.Agents.Depth > 0 {
			role.Depth = raw.Agents.Depth
		}
		if raw.Agents.Running > 0 {
			role.Running = raw.Agents.Running
		}
		definition, err := json.Marshal(role.Normalized())
		if err != nil {
			return err
		}
		if bytes.Equal(definition, before) {
			continue
		}
		if _, err := d.Registry.PutAgentRole(name, definition, false); err != nil {
			return fmt.Errorf("fold the agent policy into %s: %w", name, err)
		}
		changed = append(changed, name)
	}
	config, err := dashconfig.Load()
	if err != nil {
		return err
	}
	if err := dashconfig.Save(config); err != nil {
		return err
	}
	if len(changed) > 0 {
		d.tellPerson(context.Background(), "Agent role changed", fmt.Sprintf("Agenthail moved this computer's agent policy into the %s %s.", strings.Join(changed, ", "), map[bool]string{true: "role", false: "roles"}[len(changed) == 1]), "", "agent.role.changed")
	}
	return nil
}

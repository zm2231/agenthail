package dashconfig

import (
	"fmt"
	"sort"
	"strings"
)

// An agent decision says what happens when an agent does something that
// used to be the person's alone: it runs, it runs and the person is told on
// their devices, or the agent must ask the person first.
const (
	AgentAllow  = "allow"
	AgentNotify = "notify"
	AgentAsk    = "ask"
)

type AgentPolicy struct {
	Actions map[string]string `json:"actions,omitempty"`
	// Depth is how deep sessions may nest below the one a person started; unset is 3.
	Depth int `json:"depth,omitempty"`
	// Running is how many sessions one agent may have running at once; unset is 8.
	Running int `json:"running,omitempty"`
}

const (
	defaultAgentDepth   = 3
	defaultAgentRunning = 8
)

type agentPolicyAction struct {
	Name    string
	Default string
	Detail  string
}

// A new action is one more row here. Actions outside this list, such as
// approving extensions or answering for the person, stay the person's.
var agentPolicyActions = []agentPolicyAction{
	{"session-create-remote", AgentNotify, "Start a session on a paired computer"},
	{"extension-build", AgentNotify, "Start an extension builder"},
	{"project-add", AgentAllow, "Add a folder on this computer that is in no project (a lead only)"},
	{"project-rename", AgentAllow, "Rename its own project (a lead only)"},
	{"project-link", AgentAllow, "Link a project into its own (a lead only)"},
	{"project-unlink", AgentAllow, "Unlink a project from its own (a lead only)"},
	{"project-setting", AgentAllow, "Turn its own project's tools on or off (a lead only)"},
	{"project-archive", AgentNotify, "Archive its own project (a lead only)"},
	{"project-unarchive", AgentAllow, "Unarchive its own project (a lead only)"},
	{"project-remove", AgentNotify, "Remove its own project from Agenthail; the files stay (a lead only)"},
	{"coordinator-set", AgentAllow, "Make a session in its own project the lead (a lead only)"},
	{"coordinator-new", AgentAllow, "Start a new lead for its own project (a lead only)"},
	{"coordinator-clear", AgentNotify, "Clear its own project's lead (a lead only)"},
	{"relay-add", AgentAllow, "Relay replies between sessions in its own project (a lead only)"},
	{"relay-remove", AgentAllow, "Remove such a relay (a lead only)"},
	{"queue-cancel", AgentAllow, "Cancel a queued message in its own project (a lead only)"},
	{"queue-clear", AgentAllow, "Clear queued messages in its own project (a lead only)"},
	{"channel-create", AgentAllow, "Create a channel (a lead only)"},
	{"channel-rename", AgentAllow, "Rename a channel whose sessions are all in its project (a lead only)"},
	{"channel-add", AgentAllow, "Add a session in its project to a channel whose sessions are all in its project (a lead only)"},
	{"channel-remove", AgentAllow, "Remove a session from a channel whose sessions are all in its project (a lead only)"},
	{"channel-delete", AgentNotify, "Delete a channel whose sessions are all in its project (a lead only)"},
	{"limit-resume", AgentNotify, "Turn resuming its own project's sessions a usage limit stopped on or off (a lead only)"},
	{"cross-project-message", AgentAllow, "Send a queued message to a session in another project on this computer"},
	{"cross-project-steer", AgentNotify, "Steer, interrupt or send now to a session in another project on this computer, within its ceiling"},
}

func agentPolicyActionNamed(name string) (agentPolicyAction, bool) {
	for _, action := range agentPolicyActions {
		if action.Name == name {
			return action, true
		}
	}
	return agentPolicyAction{}, false
}

func (p *AgentPolicy) Decision(name string) string {
	action, known := agentPolicyActionNamed(name)
	if !known {
		return AgentAsk
	}
	if p != nil {
		if value, set := p.Actions[name]; set && validAgentDecision(value) {
			return value
		}
	}
	return action.Default
}

func (p *AgentPolicy) DepthLimit() int {
	if p == nil || p.Depth <= 0 {
		return defaultAgentDepth
	}
	return p.Depth
}

func (p *AgentPolicy) RunningLimit() int {
	if p == nil || p.Running <= 0 {
		return defaultAgentRunning
	}
	return p.Running
}

func validAgentDecision(value string) bool {
	return value == AgentAllow || value == AgentNotify || value == AgentAsk
}

type AgentPolicyRow struct {
	Action   string `json:"action"`
	Detail   string `json:"detail"`
	Default  string `json:"default"`
	Decision string `json:"decision"`
}

func (p *AgentPolicy) Rows() []AgentPolicyRow {
	rows := make([]AgentPolicyRow, 0, len(agentPolicyActions))
	for _, action := range agentPolicyActions {
		rows = append(rows, AgentPolicyRow{Action: action.Name, Detail: action.Detail, Default: action.Default, Decision: p.Decision(action.Name)})
	}
	return rows
}

func (c *Config) SetAgentDecision(name, decision string) error {
	action, known := agentPolicyActionNamed(name)
	if !known {
		names := make([]string, 0, len(agentPolicyActions))
		for _, row := range agentPolicyActions {
			names = append(names, row.Name)
		}
		sort.Strings(names)
		return fmt.Errorf("no agent action is named %q; the actions are %s", name, strings.Join(names, ", "))
	}
	if decision != "" && !validAgentDecision(decision) {
		return fmt.Errorf("a decision is allow, notify or ask, not %q", decision)
	}
	if c.Agents == nil {
		c.Agents = &AgentPolicy{}
	}
	if decision == "" || decision == action.Default {
		delete(c.Agents.Actions, name)
	} else {
		if c.Agents.Actions == nil {
			c.Agents.Actions = map[string]string{}
		}
		c.Agents.Actions[name] = decision
	}
	c.compactAgents()
	return nil
}

func (c *Config) SetAgentLimit(name string, value int) error {
	if value < 0 {
		return fmt.Errorf("a limit is 0 (the default) or more, not %d", value)
	}
	if c.Agents == nil {
		c.Agents = &AgentPolicy{}
	}
	switch name {
	case "depth":
		c.Agents.Depth = value
	case "running":
		c.Agents.Running = value
	default:
		return fmt.Errorf("a limit is depth or running, not %q", name)
	}
	c.compactAgents()
	return nil
}

func (c *Config) compactAgents() {
	if c.Agents != nil && len(c.Agents.Actions) == 0 && c.Agents.Depth == 0 && c.Agents.Running == 0 {
		c.Agents = nil
	}
}

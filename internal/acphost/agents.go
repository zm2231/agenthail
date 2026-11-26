package acphost

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"slices"

	"github.com/zm2231/agenthail/internal/surface"
)

type Agent struct {
	ID         string            `json:"id"`
	Label      string            `json:"label,omitempty"`
	Command    []string          `json:"command,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Modes      map[string]string `json:"modes,omitempty"`
	Disabled   bool              `json:"disabled,omitempty"`
	Configured bool              `json:"-"`
}

var (
	agentIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)
	reservedIDs    = []string{"claude", "codex", "notion", "agent", "agenthail"}
	ceilingRanks   = []string{"plan", "ask", "edits", "auto", "full"}
)

func BuiltInAgents() []Agent {
	return []Agent{{
		ID:      "devin",
		Label:   "Devin",
		Command: []string{"devin", "acp", "--process-model", "in-process"},
		Modes:   map[string]string{"ask": "plan", "plan": "plan", "accept-edits": "edits", "smart": "auto", "bypass": "full"},
	}}
}

// LoadAgents returns the built-in agents with the config's entries applied over them, and a problem for each entry it skipped.
func LoadAgents(home string) ([]Agent, []error) {
	agents := BuiltInAgents()
	config, err := surface.ReadAgenthailConfig(home)
	if err != nil {
		return agents, []error{err}
	}
	raw, ok := config["acpAgents"]
	if !ok {
		return agents, nil
	}
	var entries []Agent
	if err := json.Unmarshal(raw, &entries); err != nil {
		return agents, []error{fmt.Errorf("acpAgents must be a list of {\"id\", \"label\", \"command\"} entries: %w", err)}
	}
	var problems []error
	for _, entry := range entries {
		entry.Configured = true
		if err := entry.validate(); err != nil {
			problems = append(problems, err)
			continue
		}
		index := slices.IndexFunc(agents, func(agent Agent) bool { return agent.ID == entry.ID })
		switch {
		case entry.Disabled && index >= 0:
			agents = slices.Delete(agents, index, index+1)
		case entry.Disabled:
		case index >= 0:
			agents[index] = entry
		default:
			agents = append(agents, entry)
		}
	}
	return agents, problems
}

func (a Agent) validate() error {
	if !agentIDPattern.MatchString(a.ID) || slices.Contains(reservedIDs, a.ID) {
		return fmt.Errorf("acpAgents entry %q: the id must be lowercase letters, digits and -, at most 32 characters, and not %v", a.ID, reservedIDs)
	}
	if a.Disabled {
		return nil
	}
	if len(a.Command) == 0 || a.Command[0] == "" {
		return fmt.Errorf("acpAgents entry %q has no command", a.ID)
	}
	for mode, rank := range a.Modes {
		if !slices.Contains(ceilingRanks, rank) {
			return fmt.Errorf("acpAgents entry %q ranks mode %q as %q; ranks are %v", a.ID, mode, rank, ceilingRanks)
		}
	}
	return nil
}

func (a Agent) DisplayName() string {
	if a.Label != "" {
		return a.Label
	}
	return a.ID
}

func (a Agent) BuiltIn() bool {
	return !a.Configured && slices.ContainsFunc(BuiltInAgents(), func(b Agent) bool { return b.ID == a.ID })
}

func (a Agent) Installed() bool {
	_, err := exec.LookPath(a.Command[0])
	return err == nil
}

// ModeRank is the ceiling rank of one of the agent's modes; a mode the entry does not rank is full.
func (a Agent) ModeRank(mode string) string {
	if rank, ok := a.Modes[mode]; ok {
		return rank
	}
	return "full"
}

func RankIndex(rank string) int { return slices.Index(ceilingRanks, rank) }

func ValidAgentID(id string) bool {
	return agentIDPattern.MatchString(id) && !slices.Contains(reservedIDs, id)
}

package daemon

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/claudepick"
)

type ClaudeAccountsConfig struct {
	Auto           *bool   `json:"auto,omitempty"`
	Preferred      string  `json:"preferred,omitempty"`
	SessionCeiling float64 `json:"sessionCeiling,omitempty"`
	WeeklyCeiling  float64 `json:"weeklyCeiling,omitempty"`
	AutoMove       bool    `json:"autoMove,omitempty"`
}

func (c DashboardConfig) ClaudeAccountPolicy() claudepick.Policy {
	policy := claudepick.DefaultPolicy()
	if c.ClaudeAccounts == nil {
		return policy
	}
	if c.ClaudeAccounts.Auto != nil {
		policy.Auto = *c.ClaudeAccounts.Auto
	}
	policy.Preferred = c.ClaudeAccounts.Preferred
	policy.AutoMove = c.ClaudeAccounts.AutoMove
	if c.ClaudeAccounts.SessionCeiling > 0 {
		policy.SessionCeiling = c.ClaudeAccounts.SessionCeiling
	}
	if c.ClaudeAccounts.WeeklyCeiling > 0 {
		policy.WeeklyCeiling = c.ClaudeAccounts.WeeklyCeiling
	}
	return policy
}

func (c *DashboardConfig) SetClaudeAccountPolicy(policy claudepick.Policy) error {
	for _, ceiling := range []float64{policy.SessionCeiling, policy.WeeklyCeiling} {
		if ceiling <= 0 || ceiling > 100 {
			return fmt.Errorf("a ceiling is a percentage from 1 to 100")
		}
	}
	auto := policy.Auto
	c.ClaudeAccounts = &ClaudeAccountsConfig{Auto: &auto, Preferred: policy.Preferred, SessionCeiling: policy.SessionCeiling, WeeklyCeiling: policy.WeeklyCeiling, AutoMove: policy.AutoMove}
	return nil
}

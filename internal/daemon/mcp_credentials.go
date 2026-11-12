package daemon

import (
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var defaultSessionCapabilities = []string{"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write", "browser.drive", "simulator.drive"}

const leadCapability = "control.project"

var operatorCapabilities = []string{"control.all", "read.all"}

func sessionCapabilities(roles registry.SessionRoles) []string {
	capabilities := slices.Clone(defaultSessionCapabilities)
	if roles.Lead {
		capabilities = append(capabilities, leadCapability)
	}
	if roles.Operator {
		capabilities = append(capabilities, operatorCapabilities...)
	}
	return capabilities
}

func (d *Daemon) capabilitiesOf(sessionID string) ([]string, error) {
	roles, err := d.Registry.LocalSessionRoles(sessionID)
	return sessionCapabilities(roles), err
}

func (d *Daemon) issueSessionCredential(session surface.Session) (registry.IssuedCredential, error) {
	return d.Registry.IssueSessionCredential(session.ID, time.Now())
}

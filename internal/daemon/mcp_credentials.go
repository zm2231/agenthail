package daemon

import (
	"encoding/json"
	"slices"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var defaultSessionCapabilities = []string{"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write"}

const leadCapability = "control.project"

func sessionCapabilities(lead bool) []string {
	capabilities := slices.Clone(defaultSessionCapabilities)
	if lead {
		capabilities = append(capabilities, leadCapability)
	}
	return capabilities
}

func (d *Daemon) capabilitiesOf(sessionID string) ([]string, error) {
	lead, err := d.Registry.IsLocalLead(sessionID)
	return sessionCapabilities(lead), err
}

func (d *Daemon) issueSessionCredential(session surface.Session) (registry.IssuedCredential, error) {
	capabilities, err := d.capabilitiesOf(session.ID)
	if err != nil {
		return registry.IssuedCredential{}, err
	}
	return d.Registry.IssueSessionCredential(session.ID, capabilities, json.RawMessage(`{}`), time.Now())
}

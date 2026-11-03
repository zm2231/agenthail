package daemon

import (
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var defaultSessionCapabilities = []string{"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write"}

func (d *Daemon) issueSessionCredential(session surface.Session) (registry.IssuedCredential, error) {
	capabilities := append([]string(nil), defaultSessionCapabilities...)
	if project, _ := d.sessionProject(session); project != nil {
		if spec, err := d.Registry.Coordinator(project.ID); err == nil && spec.SessionID == session.ID {
			capabilities = append(capabilities, "control.project")
		}
	}
	return d.Registry.IssueSessionCredential(session.ID, capabilities, json.RawMessage(`{}`), time.Now())
}

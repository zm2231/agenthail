package daemon

import (
	"encoding/json"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

var defaultSessionCapabilities = []string{"read.self", "read.project", "spawn", "control.descendants", "requests.answer", "ledger.read", "ledger.write"}

func (d *Daemon) issueSessionCredential(session surface.Session) (registry.IssuedCredential, error) {
	return d.Registry.IssueSessionCredential(session.ID, d.sessionCapabilities(session), json.RawMessage(`{}`), time.Now())
}

package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) issueSessionCredential(session surface.Session) (registry.IssuedCredential, error) {
	return d.Registry.IssueSessionCredential(session.ID, time.Now())
}

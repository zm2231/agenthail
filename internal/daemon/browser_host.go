package daemon

import (
	"context"

	"github.com/zm2231/agenthail/internal/browserhost"
)

func (d *Daemon) SetBrowserHost(manager *browserhost.Manager) {
	manager.Changed = func() {
		if d.dashboard != nil {
			d.dashboard.invalidate()
		}
	}
	d.browserHost = manager
}

func (d *Daemon) superviseBrowserHost(ctx context.Context) {
	if d.browserHost != nil {
		d.browserHost.Run(ctx)
	}
}

func (d *Daemon) browserStatus() *browserhost.Status {
	if d.browserHost == nil {
		return nil
	}
	status := d.browserHost.Status()
	return &status
}

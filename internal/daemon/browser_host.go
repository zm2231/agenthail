package daemon

import (
	"context"
	"errors"
	"log"
	"time"

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

func (d *Daemon) closeBrowserTabs(sessionID string) {
	if d.browserHost == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := d.browserHost.Client().Call(ctx, "tabs.closeOwner", map[string]any{"owner": sessionID}, nil)
	if err != nil && !errors.Is(err, browserhost.ErrNotRunning) {
		log.Printf("close the browser tabs of %s: %v", sessionID, err)
	}
}

func (d *Daemon) browserStatus() *browserhost.Status {
	if d.browserHost == nil {
		return nil
	}
	status := d.browserHost.Status()
	return &status
}

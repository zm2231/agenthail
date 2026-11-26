package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/acphost"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/acpsurface"
)

const acpHostCheckInterval = 30 * time.Second

func (d *Daemon) SetACPHost(manager *acphost.Manager) {
	d.acpHost = manager
}

func (d *Daemon) superviseACPHost(ctx context.Context) {
	if d.acpHost == nil || !d.hasACPAgents() {
		return
	}
	go func() {
		client := d.acpHost.Client()
		for ctx.Err() == nil {
			_ = client.Subscribe(ctx, func(acphost.Event) { d.requestCatalogDiscovery() })
			select {
			case <-ctx.Done():
			case <-time.After(2 * time.Second):
			}
		}
	}()
	ticker := time.NewTicker(acpHostCheckInterval)
	defer ticker.Stop()
	for {
		ensure, cancel := context.WithTimeout(ctx, 20*time.Second)
		if err := d.acpHost.Ensure(ensure); err != nil && ctx.Err() == nil {
			d.log.Printf("ACP host: %v (log %s)", err, acphost.LogPath(d.acpHost.Home))
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (d *Daemon) acpAgent(kind surface.SurfaceKind) (*acpsurface.ACP, bool) {
	adapter, ok := d.adapters().ForSurface(kind).(*acpsurface.ACP)
	return adapter, ok
}

func (d *Daemon) hasACPAgents() bool {
	for _, adapter := range d.Surfaces {
		if _, ok := adapter.(*acpsurface.ACP); ok {
			return true
		}
	}
	return false
}

func (d *Daemon) isACPAgent(kind surface.SurfaceKind) bool {
	_, ok := d.acpAgent(kind)
	return ok
}

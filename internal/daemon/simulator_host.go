package daemon

import (
	"context"
	"log"
	"time"

	"github.com/zm2231/agenthail/internal/simhost"
)

func (d *Daemon) SetSimulatorHost(host *simhost.Host) {
	d.simHost = host
}

func (d *Daemon) sweepSimulatorLeases(ctx context.Context) {
	if d.simHost == nil || !d.simHost.Local() {
		return
	}
	d.simulatorStatus(ctx)
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if ended := d.simHost.Sweep(); len(ended) > 0 {
				log.Printf("simulator leases ended unused or at their limit: %v", ended)
			}
		}
	}
}

func (d *Daemon) releaseSimulators(sessionID string) {
	if d.simHost == nil {
		return
	}
	if !d.simHost.Local() {
		go d.releaseForwardedSimulators(sessionID)
		return
	}
	detached := d.simHost.Detach(sessionID, "")
	go func() {
		d.simHost.End(detached)
		d.simHost.RemoveIdleOwner(sessionID)
	}()
}

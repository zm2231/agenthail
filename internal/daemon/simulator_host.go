package daemon

import (
	"context"
	"log"
	"sync"
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

// releaseSimulators takes the session's simulator lock, so it waits for a call still running and a restored session's next call waits for it.
func (d *Daemon) releaseSimulators(sessionID string) {
	if d.simHost == nil {
		return
	}
	go func() {
		lock := d.simulatorOwnerLock(sessionID)
		lock.Lock()
		defer lock.Unlock()
		if !d.simHost.Local() {
			d.releaseForwardedSimulators(sessionID)
			return
		}
		d.simHost.Release(sessionID, "")
		d.simHost.RemoveIdleOwner(sessionID)
	}()
}

func (d *Daemon) simulatorOwnerLock(owner string) *sync.Mutex {
	d.simStatusMu.Lock()
	defer d.simStatusMu.Unlock()
	if d.simOwners == nil {
		d.simOwners = map[string]*sync.Mutex{}
	}
	if d.simOwners[owner] == nil {
		d.simOwners[owner] = &sync.Mutex{}
	}
	return d.simOwners[owner]
}

func (d *Daemon) callSimulator(ctx context.Context, owner string, req simhost.Request) (map[string]any, error) {
	lock := d.simulatorOwnerLock(owner)
	lock.Lock()
	defer lock.Unlock()
	if !d.simHost.Local() {
		return d.forwardSimulator(ctx, owner, req)
	}
	return d.simHost.Call(ctx, owner, req)
}

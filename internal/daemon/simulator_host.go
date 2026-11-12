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

type simulatorOwner struct {
	mu      sync.Mutex
	refs    int
	release chan struct{}
}

// releaseSimulators marks the release before archive returns, so a restored session's next call waits for it, and runs it once any call still running is done.
func (d *Daemon) releaseSimulators(sessionID string) {
	if d.simHost == nil {
		return
	}
	owner, done := d.holdSimulatorOwner(sessionID)
	released := make(chan struct{})
	d.simOwnersMu.Lock()
	previous := owner.release
	owner.release = released
	d.simOwnersMu.Unlock()
	go func() {
		defer done()
		defer close(released)
		if previous != nil {
			<-previous
		}
		owner.mu.Lock()
		defer owner.mu.Unlock()
		if !d.simHost.Local() {
			d.releaseForwardedSimulators(sessionID)
			return
		}
		d.simHost.Release(sessionID, "")
		d.simHost.RemoveIdleOwner(sessionID)
	}()
}

func (d *Daemon) holdSimulatorOwner(id string) (*simulatorOwner, func()) {
	d.simOwnersMu.Lock()
	defer d.simOwnersMu.Unlock()
	if d.simOwners == nil {
		d.simOwners = map[string]*simulatorOwner{}
	}
	owner := d.simOwners[id]
	if owner == nil {
		owner = &simulatorOwner{}
		d.simOwners[id] = owner
	}
	owner.refs++
	return owner, func() {
		d.simOwnersMu.Lock()
		defer d.simOwnersMu.Unlock()
		if owner.refs--; owner.refs == 0 && d.simOwners[id] == owner {
			delete(d.simOwners, id)
		}
	}
}

func (d *Daemon) callSimulator(ctx context.Context, id string, req simhost.Request) (map[string]any, error) {
	owner, done := d.holdSimulatorOwner(id)
	defer done()
	d.simOwnersMu.Lock()
	pending := owner.release
	d.simOwnersMu.Unlock()
	if pending != nil {
		select {
		case <-pending:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	owner.mu.Lock()
	defer owner.mu.Unlock()
	if !d.simHost.Local() {
		return d.forwardSimulator(ctx, id, req)
	}
	return d.simHost.Call(ctx, id, req)
}

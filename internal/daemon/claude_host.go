package daemon

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	claudeHostCheckInterval  = 30 * time.Second
	claudeHostEnsureTimeout  = 20 * time.Second
	claudeHostWatchRetryWait = 2 * time.Second
)

func (d *Daemon) SetClaudeHost(manager *claudehost.Manager) {
	d.claudeHost = manager
}

// superviseClaudeHost keeps one host of this build running: it attaches to a
// running host on start, starts one when none answers, and after an update
// retires the older host and starts this build's at once. A retiring host
// keeps serving its sessions, so its requests are followed until it exits.
func (d *Daemon) superviseClaudeHost(ctx context.Context) {
	if d.claudeHost == nil {
		return
	}
	var watchers sync.WaitGroup
	defer watchers.Wait()
	watchers.Add(1)
	go func() {
		defer watchers.Done()
		d.watchClaudeHostRequests(ctx)
	}()
	retiring := map[string]bool{}
	var retiringMu sync.Mutex
	ticker := time.NewTicker(claudeHostCheckInterval)
	defer ticker.Stop()
	var last claudeHostCheck
	for {
		last = d.checkClaudeHost(ctx, last)
		for _, host := range d.claudeHost.Client().Retiring() {
			retiringMu.Lock()
			watching := retiring[host.Socket]
			retiring[host.Socket] = true
			retiringMu.Unlock()
			if watching {
				continue
			}
			watchers.Add(1)
			go func(host claudehost.Client) {
				defer watchers.Done()
				d.watchRetiringClaudeHost(ctx, host)
				retiringMu.Lock()
				delete(retiring, host.Socket)
				retiringMu.Unlock()
			}(host)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

type claudeHostListing interface{ HostChanged() }

// claudeHostChanged drops the Claude surface's cached host listing, then asks
// for a catalog pass that reads the host again.
func (d *Daemon) claudeHostChanged() {
	if listing, ok := d.adapters().ForSurface(surface.KindClaude).(claudeHostListing); ok {
		listing.HostChanged()
	}
	d.requestCatalogDiscovery()
}

// watchClaudeHostRequests asks for a catalog pass whenever the host opens or
// resolves a request or a session's background tasks change, so a session
// shows what it waits on and runs as soon as Claude reports it rather than at
// the next scheduled pass. A dropped subscription may
// mean a new host, which numbers its events from the start again, so the
// next one reads the host's whole buffer; repeated passes coalesce.
func (d *Daemon) watchClaudeHostRequests(ctx context.Context) {
	client := claudehost.NewClient(d.claudeHost.Home)
	var after uint64
	for ctx.Err() == nil {
		start, _ := client.Subscribe(ctx, after, func(event claudehost.Event) {
			d.recordPeerHold(event)
			if event.Kind == "request_opened" || event.Kind == "request_resolved" || event.Kind == "tasks" {
				d.claudeHostChanged()
			}
		})
		if start.Gap {
			after = start.Seq
			d.claudeHostChanged()
			continue
		}
		after = 0
		select {
		case <-ctx.Done():
		case <-time.After(claudeHostWatchRetryWait):
		}
	}
}

// watchRetiringClaudeHost follows a retiring host's requests until it exits.
// Its exit ends the requests it held, so the catalog is read again then.
func (d *Daemon) watchRetiringClaudeHost(ctx context.Context, host claudehost.Client) {
	d.log.Printf("Claude host: following the retiring host on %s until its sessions close", host.Socket)
	var after uint64
	for ctx.Err() == nil {
		start, _ := host.Subscribe(ctx, after, func(event claudehost.Event) {
			after = event.Seq
			d.recordPeerHold(event)
			if event.Kind == "request_opened" || event.Kind == "request_resolved" || event.Kind == "tasks" || event.Kind == "exited" {
				d.claudeHostChanged()
			}
		})
		if start.Gap {
			after = start.Seq
			d.claudeHostChanged()
			continue
		}
		helloCtx, cancel := context.WithTimeout(ctx, time.Second)
		_, err := host.Hello(helloCtx)
		cancel()
		if err != nil {
			break
		}
		select {
		case <-ctx.Done():
		case <-time.After(claudeHostWatchRetryWait):
		}
	}
	if ctx.Err() == nil {
		d.log.Printf("Claude host: the retiring host on %s has exited", host.Socket)
		d.claudeHostChanged()
	}
}

type claudeHostCheck struct {
	status string
	pid    int
}

// checkClaudeHost ensures the host runs and logs what changed since the last
// check. A host that was running and is replaced by another one exited
// without being asked to, so the log names both and where the old one wrote.
func (d *Daemon) checkClaudeHost(ctx context.Context, last claudeHostCheck) claudeHostCheck {
	ensureCtx, cancel := context.WithTimeout(ctx, claudeHostEnsureTimeout)
	defer cancel()
	check := claudeHostCheck{status: "running"}
	client, err := d.claudeHost.Ensure(ensureCtx)
	if err == nil {
		var hello claudehost.Hello
		hello, err = client.Hello(ensureCtx)
		check.pid = hello.PID
	}
	if err != nil {
		check.status = err.Error()
	}
	if ctx.Err() != nil {
		return last
	}
	if last.status == "running" && check.status == "running" && check.pid != last.pid {
		d.log.Printf("Claude host: pid %d is gone and pid %d now runs; see %s", last.pid, check.pid, d.claudeHost.LogPath())
	}
	if check.status != last.status {
		d.log.Printf("Claude host: %s", check.status)
	}
	return check
}

// peerHold is what Claude reports when its inbound policy parks a message
// instead of passing it to the model, and when it later releases or drops it.
type peerHold struct {
	Type        string `json:"type"`
	Subtype     string `json:"subtype"`
	State       string `json:"state"`
	Cause       string `json:"cause"`
	Outcome     string `json:"outcome"`
	MessageUUID string `json:"message_uuid"`
}

// recordPeerHold keeps a delivery's state equal to what the target's Claude
// did with it, so a held message is a problem rather than a silent loss.
func (d *Daemon) recordPeerHold(event claudehost.Event) {
	if event.Kind != "sdk" || d.Registry == nil {
		return
	}
	var hold peerHold
	if json.Unmarshal(event.Message, &hold) != nil || hold.Type != "system" || hold.Subtype != "peer_message_hold" || hold.MessageUUID == "" {
		return
	}
	message := registry.PeerMessage{MsgID: hold.MessageUUID, TargetSessionID: event.SessionID}
	var changed bool
	var err error
	switch hold.State {
	case "held":
		changed, err = d.Registry.HoldPeerMessage(message, registry.PeerHeldReason(hold.Cause))
	case "released":
		changed, err = d.Registry.ReleasePeerMessage(message)
	case "dropped":
		changed, err = d.Registry.DropPeerMessage(message, registry.PeerDroppedReason(hold.Outcome))
	}
	if err != nil {
		d.log.Printf("record a message Claude %s for %s: %s", hold.State, event.SessionID, err)
	}
	if !changed {
		return
	}
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish delivery problem: %s", err)
	}
	d.publishEvent("state.changed", event.SessionID, map[string]string{"source": "delivery-problem"})
}

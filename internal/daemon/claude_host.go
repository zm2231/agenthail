package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
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

// watchClaudeHostRequests asks for a catalog pass whenever the host opens or
// resolves a request, so a session shows what it waits on as soon as Claude
// asks rather than at the next scheduled pass. A dropped subscription may
// mean a new host, which numbers its events from the start again, so the
// next one reads the host's whole buffer; repeated passes coalesce.
func (d *Daemon) watchClaudeHostRequests(ctx context.Context) {
	client := claudehost.NewClient(d.claudeHost.Home)
	var after uint64
	for ctx.Err() == nil {
		start, _ := client.Subscribe(ctx, after, func(event claudehost.Event) {
			if event.Kind == "request_opened" || event.Kind == "request_resolved" {
				d.requestCatalogDiscovery()
			}
		})
		if start.Gap {
			after = start.Seq
			d.requestCatalogDiscovery()
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
	_, _ = host.Subscribe(ctx, 0, func(event claudehost.Event) {
		if event.Kind == "request_opened" || event.Kind == "request_resolved" || event.Kind == "exited" {
			d.requestCatalogDiscovery()
		}
	})
	if ctx.Err() == nil {
		d.log.Printf("Claude host: the retiring host on %s has exited", host.Socket)
		d.requestCatalogDiscovery()
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

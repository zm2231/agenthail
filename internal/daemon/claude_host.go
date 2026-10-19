package daemon

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
)

const (
	claudeHostCheckInterval = 30 * time.Second
	claudeHostEnsureTimeout = 20 * time.Second
)

func (d *Daemon) SetClaudeHost(manager *claudehost.Manager) {
	d.claudeHost = manager
}

// superviseClaudeHost keeps one host of this build running: it attaches to a
// running host on start, starts one when none answers, and after an update
// starts this build's host once the older one has drained.
func (d *Daemon) superviseClaudeHost(ctx context.Context) {
	if d.claudeHost == nil {
		return
	}
	ticker := time.NewTicker(claudeHostCheckInterval)
	defer ticker.Stop()
	var last claudeHostCheck
	for {
		last = d.checkClaudeHost(ctx, last)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
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

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
	reported := ""
	for {
		ensureCtx, cancel := context.WithTimeout(ctx, claudeHostEnsureTimeout)
		_, err := d.claudeHost.Ensure(ensureCtx)
		cancel()
		status := "running"
		if err != nil {
			status = err.Error()
		}
		if status != reported && ctx.Err() == nil {
			d.log.Printf("Claude host: %s", status)
			reported = status
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

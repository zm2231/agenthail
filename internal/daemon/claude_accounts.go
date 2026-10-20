package daemon

import (
	"context"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const claudeAccountsTTL = 5 * time.Second

// claudeAccounts is read once per few seconds rather than per projected
// row: a catalog pass projects every session, and each read re-parses the
// Agenthail config.
func (d *Daemon) claudeAccounts() []surface.ClaudeAccount {
	d.claudeAccountsMu.Lock()
	defer d.claudeAccountsMu.Unlock()
	if d.claudeAccountList != nil && time.Since(d.claudeAccountsAt) < claudeAccountsTTL {
		return d.claudeAccountList
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	accounts, err := surface.ClaudeAccounts(home)
	if err != nil {
		return d.claudeAccountList
	}
	d.claudeAccountList, d.claudeAccountsAt = accounts, time.Now()
	return accounts
}

// pruneClaudeMoves removes the source copies moves retired more than
// surfaces.ClaudeMoveRetention ago.
func (d *Daemon) pruneClaudeMoves(ctx context.Context) {
	ticker := time.NewTicker(time.Hour)
	defer ticker.Stop()
	for {
		if home, err := os.UserHomeDir(); err == nil {
			if pruned, err := surfaces.PruneClaudeMoves(home, surfaces.ClaudeMoveRetention, time.Now()); err != nil {
				d.log.Printf("prune moved Claude sessions: %s", err)
			} else if len(pruned) > 0 {
				d.log.Printf("removed the recovery copies of %d moved Claude sessions", len(pruned))
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

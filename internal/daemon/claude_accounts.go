package daemon

import (
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
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

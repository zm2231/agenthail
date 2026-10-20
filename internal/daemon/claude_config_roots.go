package daemon

import (
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const claudeConfigRootsTTL = 5 * time.Second

// claudeConfigRoots is read once per few seconds rather than per projected
// row: a catalog pass projects every session, and each read re-parses the
// Agenthail config.
func (d *Daemon) claudeConfigRoots() []surface.ClaudeConfigRoot {
	d.claudeConfigRootsMu.Lock()
	defer d.claudeConfigRootsMu.Unlock()
	if d.claudeConfigRootList != nil && time.Since(d.claudeConfigRootsAt) < claudeConfigRootsTTL {
		return d.claudeConfigRootList
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	roots, err := surface.DescribeClaudeConfigRoots(home)
	if err != nil {
		return d.claudeConfigRootList
	}
	d.claudeConfigRootList, d.claudeConfigRootsAt = roots, time.Now()
	return roots
}

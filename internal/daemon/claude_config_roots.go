package daemon

import (
	"context"
	"net/http"
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

type claudeConfigRootReader interface {
	ClaudeConfigRoots(ctx context.Context) ([]surface.ClaudeConfigRoot, error)
}

// liveClaudeConfigRoots adds each root's sign-in profile and current usage,
// which only the Claude surface can read from claude.ai.
func (d *Daemon) liveClaudeConfigRoots(ctx context.Context) []surface.ClaudeConfigRoot {
	if reader, ok := d.adapters().ForSurface(surface.KindClaude).(claudeConfigRootReader); ok {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if roots, err := reader.ClaudeConfigRoots(ctx); err == nil {
			return roots
		}
	}
	return d.claudeConfigRoots()
}

func (d *Daemon) apiClaudeConfigRootsHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	roots := d.liveClaudeConfigRoots(r.Context())
	if roots == nil {
		roots = []surface.ClaudeConfigRoot{}
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"claude": roots})
}

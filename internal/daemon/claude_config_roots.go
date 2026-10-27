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
	return d.claudeConfigRootsWithin(claudeConfigRootsTTL)
}

// Usage is matched to the login a root holds now, so its readers never take a description older than this read.
func (d *Daemon) currentClaudeConfigRoots() []surface.ClaudeConfigRoot {
	return d.claudeConfigRootsWithin(0)
}

func (d *Daemon) claudeConfigRootsWithin(age time.Duration) []surface.ClaudeConfigRoot {
	d.claudeConfigRootsMu.Lock()
	defer d.claudeConfigRootsMu.Unlock()
	if d.claudeConfigRootList != nil && time.Since(d.claudeConfigRootsAt) < age {
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
	ClaudeConfigRoot(ctx context.Context, path string) (surface.ClaudeConfigRoot, error)
}

// claudeRootUsageTimeout bounds one root's live usage read, which a
// new-session form fills in after it has shown the roots.
const claudeRootUsageTimeout = 5 * time.Second

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

// claudeRootUsageHandler answers one root's current usage. It is read scoped,
// so it says that a login has no sign-in profile without naming the profiles
// or their accounts, which /api/v1/claude/roots reports under settings.
func (d *Daemon) claudeRootUsageHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for this endpoint.")
		return
	}
	path := r.URL.Query().Get("root")
	reader, ok := d.adapters().ForSurface(surface.KindClaude).(claudeConfigRootReader)
	if !ok {
		writeAPIError(w, http.StatusServiceUnavailable, "claude_unavailable", "Claude is not available on this host.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), claudeRootUsageTimeout)
	defer cancel()
	root, err := reader.ClaudeConfigRoot(ctx, path)
	if err != nil {
		writeAPIError(w, http.StatusNotFound, "root_not_found", err.Error())
		return
	}
	if root.Proxy != "" {
		writeDashboardJSON(w, http.StatusOK, d.proxyRootUsage(ctx, root, time.Now()))
		return
	}
	problem := root.UsageProblem
	if problem == "" && root.SigninProblem != "" {
		problem = "no browser sign-in profile here is signed into this login on claude.ai; agenthail signin list shows which accounts they hold"
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"root": root.Root, "usage": root.Usage, "usageProblem": problem})
}

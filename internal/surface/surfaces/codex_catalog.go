package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// CatalogList lists loaded and recent threads on every pass. On the first
// pass and then every codexCatalogSweepInterval it pages through every thread
// Codex holds; only such a sweep is complete, so only it can retire threads
// Codex no longer has.
func (c *Codex) CatalogList(ctx context.Context) ([]surface.Session, bool, error) {
	c.sweepMu.Lock()
	sweep := c.lastSweep.IsZero() || time.Since(c.lastSweep) >= codexCatalogSweepInterval
	c.sweepMu.Unlock()
	started := time.Now()
	sessions, complete, err := c.list(ctx, sweep)
	if err == nil && complete {
		c.sweepMu.Lock()
		c.lastSweep = started
		c.sweepMu.Unlock()
	}
	return sessions, complete, err
}

// CatalogWatchDirs are the folders where a new thread's rollout file
// appears: Codex's sessions folder and its newest year, month and day
// folders. Watching each level means the folder for a new day is watched as
// soon as Codex creates it, before its first rollout lands.
func (c *Codex) CatalogWatchDirs() []string {
	dir := filepath.Join(codexHome(), "sessions")
	dirs := []string{dir}
	for range 3 {
		entries, err := os.ReadDir(dir)
		if err != nil {
			break
		}
		newest := ""
		for _, entry := range entries {
			if entry.IsDir() && entry.Name() > newest {
				newest = entry.Name()
			}
		}
		if newest == "" {
			break
		}
		dir = filepath.Join(dir, newest)
		dirs = append(dirs, dir)
	}
	return dirs
}

// Presence counts a Codex session current while it works, has queued messages,
// or was active within the dashboard's Codex recency window, unless Codex has
// not loaded it.
func (c *Codex) Presence(sess surface.Session, input surface.PresenceInput) (bool, string) {
	if sess.Status == surface.StatusBusy {
		return true, "working"
	}
	if input.QueueCount > 0 {
		return true, "queued"
	}
	if sess.Status == surface.SessionStatus("notLoaded") || sess.LastActive.IsZero() {
		return false, ""
	}
	if input.Now.Sub(sess.LastActive) <= time.Duration(input.CodexRecentHours)*time.Hour {
		return true, "recent"
	}
	return false, ""
}

const (
	codexRecentListLimit      = 50
	codexSweepPageLimit       = 100
	codexCatalogSweepInterval = 5 * time.Minute
)

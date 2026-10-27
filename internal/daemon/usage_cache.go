package daemon

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const claudeUsageTTL = time.Minute

type claudeUsageCache struct {
	mu      sync.Mutex
	entries map[string]*claudeUsageEntry
}

type claudeUsageEntry struct {
	mu      sync.Mutex
	login   string
	readAt  time.Time
	live    surface.ClaudeConfigRoot
	err     error
	lastGot *surface.Usage
	last    atomic.Pointer[claudeUsageSnapshot]
}

type claudeUsageSnapshot struct {
	login  string
	readAt time.Time
	usage  *surface.Usage
}

func (c *claudeUsageCache) entry(root string) *claudeUsageEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]*claudeUsageEntry{}
	}
	entry, ok := c.entries[root]
	if !ok {
		entry = &claudeUsageEntry{}
		c.entries[root] = entry
	}
	return entry
}

// The entry lock is held across the read so concurrent polls share one claude.ai call.
func (c *claudeUsageCache) read(ctx context.Context, reader claudeConfigRootReader, root surface.ClaudeConfigRoot, now time.Time) (surface.ClaudeConfigRoot, *surface.Usage, error) {
	entry := c.entry(root.Root)
	entry.mu.Lock()
	defer entry.mu.Unlock()
	login := claudeLoginKey(root)
	if entry.login != login {
		entry.login, entry.readAt, entry.live, entry.err, entry.lastGot = login, time.Time{}, surface.ClaudeConfigRoot{}, nil, nil
	}
	if entry.readAt.IsZero() || now.Sub(entry.readAt) >= claudeUsageTTL {
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), claudeRootUsageTimeout)
		entry.live, entry.err = reader.ClaudeConfigRoot(ctx, root.Root)
		cancel()
		entry.readAt = now
		if entry.err == nil && entry.live.Usage != nil {
			entry.lastGot = entry.live.Usage
		}
		entry.last.Store(&claudeUsageSnapshot{login: login, readAt: now, usage: entry.lastGot})
	}
	return entry.live, entry.lastGot, entry.err
}

// Never waits on claude.ai; a missing or stale reading refreshes in the background.
func (c *claudeUsageCache) cached(reader claudeConfigRootReader, root surface.ClaudeConfigRoot, now time.Time) *surface.Usage {
	snapshot := c.entry(root.Root).last.Load()
	current := snapshot != nil && snapshot.login == claudeLoginKey(root)
	if !current || now.Sub(snapshot.readAt) >= claudeUsageTTL {
		go c.read(context.Background(), reader, root, now)
	}
	if !current {
		return nil
	}
	return snapshot.usage
}

func claudeLoginKey(root surface.ClaudeConfigRoot) string {
	if root.SignedInAs == nil {
		return ""
	}
	return root.SignedInAs.AccountID + "|" + root.SignedInAs.OrganizationID
}

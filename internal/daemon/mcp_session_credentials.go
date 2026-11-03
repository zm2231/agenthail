package daemon

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

const pendingCredentialLifetime = surfaceOperationTimeout + startNameMargin

// Only hashes are stored, so the raw tokens this process minted are the only ones it can send again.
type daemonMCPCredentials struct {
	d         *Daemon
	url       string
	mu        sync.Mutex
	tokens    map[string]string
	rotations keyedLocks
}

func newDaemonMCPCredentials(d *Daemon, listen string) *daemonMCPCredentials {
	return &daemonMCPCredentials{d: d, url: "http://" + listen + "/mcp", tokens: map[string]string{}}
}

func (c *daemonMCPCredentials) remember(token string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens[registry.HashSessionToken(token)] = token
}

func (c *daemonMCPCredentials) URL() string { return c.url }

func (c *daemonMCPCredentials) Pending(context.Context) (string, string, error) {
	return c.issuePending(pendingCredentialLifetime)
}

func (c *daemonMCPCredentials) issuePending(lifetime time.Duration) (string, string, error) {
	pending, err := c.d.Registry.IssuePendingCredential(time.Now(), lifetime)
	if err != nil {
		return "", "", err
	}
	c.remember(pending.Token)
	return pending.Token, pending.Ref, nil
}

func (c *daemonMCPCredentials) terminalLaunch(launcher string, agent surface.SurfaceKind) (*surface.LaunchMCPCredential, error) {
	if agent != surface.KindCodex || (launcher != surface.LauncherTMUX && launcher != surface.LauncherCMUX) {
		return nil, nil
	}
	c.d.pruneLaunchCredentials()
	token, ref, err := c.issuePending(pendingLaunchLifetime)
	if err != nil {
		return nil, err
	}
	return &surface.LaunchMCPCredential{URL: c.url, Token: token, Ref: ref}, nil
}

func (c *daemonMCPCredentials) Claim(ref, threadID string) error {
	claimed, err := c.d.Registry.ClaimPendingCredential(ref, threadID, time.Now())
	if err == nil && !claimed {
		err = fmt.Errorf("pending credential %s expired or was already claimed", ref)
	}
	return err
}

func (c *daemonMCPCredentials) Current(sessionID string) bool {
	_, ok, err := c.d.Registry.LiveCredentialHash(sessionID, time.Now())
	return err == nil && ok
}

func (c *daemonMCPCredentials) Live(sessionID string) (string, bool) {
	hash, ok, err := c.d.Registry.LiveCredentialHash(sessionID, time.Now())
	if err != nil || !ok {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	token, held := c.tokens[hash]
	return token, held
}

// Rotate returns a live token this process holds, minting a new one only when it holds none; concurrent resumes share one.
func (c *daemonMCPCredentials) Rotate(sessionID string) (string, error) {
	release, _ := c.rotations.acquire(sessionID, true)
	defer release()
	if token, ok := c.Live(sessionID); ok {
		return token, nil
	}
	issued, err := c.d.Registry.RotateSessionCredential(sessionID, time.Now())
	if err != nil {
		return "", err
	}
	c.remember(issued.Token)
	return issued.Token, nil
}

func (c *daemonMCPCredentials) Unattached(sessionID string, err error) {
	c.d.log.Printf("mcp: %s has no agenthail MCP server: %s", c.d.resolveDisplay(sessionID), err)
}

func (d *Daemon) attachMCPCredentials(listen string) {
	credentials := newDaemonMCPCredentials(d, listen)
	d.mcpCredentials.Store(credentials)
	for _, adapter := range d.Surfaces {
		if attachable, ok := adapter.(interface {
			SetMCPCredentials(surfaces.MCPCredentials)
		}); ok {
			attachable.SetMCPCredentials(credentials)
		}
	}
	if err := d.Registry.PrunePendingCredentials(time.Now()); err != nil {
		d.log.Printf("mcp: prune pending credentials: %s", err)
	}
	d.pruneLaunchCredentials()
}

func (d *Daemon) pruneLaunchCredentials() {
	if err := surface.PruneManagedCodexLaunchCredentials(time.Now().Add(-pendingLaunchLifetime)); err != nil {
		d.log.Printf("mcp: remove expired launch credential files: %s", err)
	}
}

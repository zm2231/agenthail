package daemon

import (
	"encoding/json"
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
	d      *Daemon
	url    string
	mu     sync.Mutex
	tokens map[string]string
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

func (c *daemonMCPCredentials) Pending() (string, string, error) {
	return c.issuePending(pendingCredentialLifetime)
}

func (c *daemonMCPCredentials) issuePending(lifetime time.Duration) (string, string, error) {
	pending, err := c.d.Registry.IssuePendingCredential(defaultSessionCapabilities, json.RawMessage(`{}`), time.Now(), lifetime)
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

func (c *daemonMCPCredentials) Live(sessionID string) (string, bool) {
	hash, ok, err := c.d.Registry.LiveCredentialHash(sessionID)
	if err != nil || !ok {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	token, held := c.tokens[hash]
	return token, held
}

func (c *daemonMCPCredentials) Rotate(sessionID string) (string, error) {
	session, err := c.d.Registry.Session(sessionID)
	if err != nil {
		return "", fmt.Errorf("session %s: %w", sessionID, err)
	}
	issued, err := c.d.Registry.RotateSessionCredential(session.ID, c.d.sessionCapabilities(*session), json.RawMessage(`{}`), time.Now())
	if err != nil {
		return "", err
	}
	c.remember(issued.Token)
	return issued.Token, nil
}

func (c *daemonMCPCredentials) Unattached(sessionID string, err error) {
	c.d.log.Printf("mcp: %s has no agenthail MCP server: %s", c.d.resolveDisplay(sessionID), err)
}

func (d *Daemon) sessionCapabilities(session surface.Session) []string {
	capabilities := append([]string(nil), defaultSessionCapabilities...)
	if project, _ := d.sessionProject(session); project != nil {
		if spec, err := d.Registry.Coordinator(project.ID); err == nil && spec.SessionID == session.ID {
			capabilities = append(capabilities, "control.project")
		}
	}
	return capabilities
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

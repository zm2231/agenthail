package surfaces

import (
	"context"
	"errors"
	"sync"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpServerName     = "agenthail"
	mcpToolTimeoutSec = 3700
)

var errMCPServerNotRunning = errors.New("the daemon runs no agenthail MCP server")

type MCPCredentials interface {
	URL() string
	Pending(ctx context.Context) (token, ref string, err error)
	Claim(ref, sessionID string) error
	Current(sessionID string) bool
	Live(sessionID string) (token string, ok bool)
	Rotate(sessionID string) (token string, err error)
	Unattached(sessionID string, err error)
}

type mcpAttachment struct {
	mu          sync.Mutex
	credentials MCPCredentials
	unattached  func(sessionID string, err error)
}

// OpensConversations makes this process, the daemon, the one that starts,
// forks and loads conversations. unattached hears of each one it opens without
// its agenthail MCP server.
func (a *mcpAttachment) OpensConversations(unattached func(sessionID string, err error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unattached = unattached
}

// opening is what an open carries: the credentials that attach the
// conversation's agenthail server, none in a daemon that runs no MCP server,
// or a refusal in a process that can attach none.
func (a *mcpAttachment) opening() (MCPCredentials, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.credentials == nil && a.unattached == nil {
		return nil, surface.DeliveryUnavailable(surface.ErrOpenIsTheDaemons)
	}
	return a.credentials, nil
}

func (a *mcpAttachment) openedWithoutMCP(sessionID string, err error) {
	a.mu.Lock()
	unattached := a.unattached
	a.mu.Unlock()
	if unattached != nil {
		unattached(sessionID, err)
	}
}

func (a *mcpAttachment) SetMCPCredentials(credentials MCPCredentials) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credentials = credentials
}

func (a *mcpAttachment) mcpCredentials() MCPCredentials {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credentials
}

package surface

import (
	"context"
	"errors"
	"sync"
)

const (
	MCPServerName     = "agenthail"
	MCPToolTimeoutSec = 3700
)

var ErrMCPServerNotRunning = errors.New("the daemon runs no agenthail MCP server")

type MCPCredentials interface {
	URL() string
	Pending(ctx context.Context) (token, ref string, err error)
	Claim(ref, sessionID string) error
	Current(sessionID string) bool
	Live(sessionID string) (token string, ok bool)
	Rotate(sessionID string) (token string, err error)
	Unattached(sessionID string, err error)
}

type MCPAttachment struct {
	mu          sync.Mutex
	credentials MCPCredentials
	unattached  func(sessionID string, err error)
}

// OpensConversations makes this process, the daemon, the one that starts,
// forks and loads conversations. unattached hears of each one it opens without
// its agenthail MCP server.
func (a *MCPAttachment) OpensConversations(unattached func(sessionID string, err error)) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.unattached = unattached
}

// Opening is what an open carries: the credentials that attach the
// conversation's agenthail server, none in a daemon that runs no MCP server,
// or a refusal in a process that can attach none.
func (a *MCPAttachment) Opening() (MCPCredentials, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.credentials == nil && a.unattached == nil {
		return nil, DeliveryUnavailable(ErrOpenIsTheDaemons)
	}
	return a.credentials, nil
}

func (a *MCPAttachment) OpenedWithoutMCP(sessionID string, err error) {
	a.mu.Lock()
	unattached := a.unattached
	a.mu.Unlock()
	if unattached != nil {
		unattached(sessionID, err)
	}
}

func (a *MCPAttachment) SetMCPCredentials(credentials MCPCredentials) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.credentials = credentials
}

func (a *MCPAttachment) Credentials() MCPCredentials {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.credentials
}

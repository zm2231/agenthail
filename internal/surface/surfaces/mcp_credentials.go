package surfaces

import (
	"context"
	"sync"
)

const (
	mcpServerName     = "agenthail"
	mcpToolTimeoutSec = 3700
)

type MCPCredentials interface {
	URL() string
	Pending(ctx context.Context) (token, ref string, err error)
	Claim(ref, sessionID string) error
	Live(sessionID string) (token string, ok bool)
	Rotate(sessionID string) (token string, err error)
	Unattached(sessionID string, err error)
}

type mcpAttachment struct {
	mu          sync.Mutex
	credentials MCPCredentials
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

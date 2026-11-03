package surfaces

import "sync"

const (
	mcpServerName     = "agenthail"
	mcpToolTimeoutSec = 3700
)

type MCPCredentials interface {
	URL() string
	Pending() (token, ref string, err error)
	Claim(ref, sessionID string) error
	Live(sessionID string) (token string, ok bool)
	Rotate(sessionID string) (token string, err error)
	Unattached(sessionID string, err error)
}

type mcpAttachment struct {
	mu          sync.Mutex
	credentials MCPCredentials
	stale       map[string]bool
}

func (a *mcpAttachment) markStaleMCP(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stale == nil {
		a.stale = map[string]bool{}
	}
	a.stale[sessionID] = true
}

func (a *mcpAttachment) staleMCP(sessionID string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.stale[sessionID]
}

func (a *mcpAttachment) clearStaleMCP(sessionID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.stale, sessionID)
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

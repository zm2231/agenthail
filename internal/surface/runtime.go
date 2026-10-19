package surface

import "fmt"

// RuntimeKind is what currently carries a session's conversation. A surface
// can have several (a Claude conversation runs in a CLI process or, later, the
// Claude host); the adapter that serves a session is chosen by its runtime
// kind, never by its surface alone.
type RuntimeKind string

const (
	RuntimeClaudeCLI      RuntimeKind = "claude-cli"
	RuntimeCodexDesktop   RuntimeKind = "codex-desktop"
	RuntimeCodexAppServer RuntimeKind = "codex-app-server"
	RuntimeNotionWeb      RuntimeKind = "notion-web"
	// RuntimeOperator marks Agenthail's own pseudo-sessions (the operator and
	// peer bridges), which no adapter serves.
	RuntimeOperator RuntimeKind = "agenthail"
)

// Adapters resolves the adapter that serves a session.
type Adapters []Surface

// For returns the adapter that carries the session's runtime kind. A session
// without a kind, or with one no adapter claims, is an error: it is never
// served by whichever adapter shares its surface.
func (a Adapters) For(session *Session) (Surface, error) {
	if session == nil {
		return nil, fmt.Errorf("session is required")
	}
	if session.RuntimeKind == "" {
		return nil, fmt.Errorf("session %s has no runtime kind", session.ID)
	}
	for _, adapter := range a {
		if adapter == nil {
			continue
		}
		for _, kind := range adapter.RuntimeKinds() {
			if kind == session.RuntimeKind {
				return adapter, nil
			}
		}
	}
	return nil, fmt.Errorf("no adapter serves runtime %q of session %s", session.RuntimeKind, session.ID)
}

// ForSurface returns the adapter for operations that name a surface but no
// session yet, such as creating one or resolving a surface-qualified target.
func (a Adapters) ForSurface(kind SurfaceKind) Surface {
	for _, adapter := range a {
		if adapter != nil && adapter.Name() == kind {
			return adapter
		}
	}
	return nil
}

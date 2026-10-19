package surface

import (
	"fmt"
	"strings"
)

// RuntimeKind is what currently carries a session's conversation. A surface
// can have several (a Claude conversation runs in a CLI process or in the
// Claude host); the adapter that serves a session is chosen by its runtime
// kind, never by its surface alone.
type RuntimeKind string

const (
	RuntimeClaudeCLI      RuntimeKind = "claude-cli"
	RuntimeClaudeHost     RuntimeKind = "claude-host"
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
	if err := NoReplyRoute(session); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("no adapter serves runtime %q of session %s", session.RuntimeKind, session.ID)
}

const (
	unrepliablePrefix = "unrepliable:"
	unrepliableSuffix = " (no reply address)"
)

// UnrepliableSender is the session that stands in for a session on another
// host that sent here without a route back, named by its host and session.
func UnrepliableSender(hostID, sessionID, name string) Session {
	return Session{ID: unrepliablePrefix + hostID + ":" + sessionID, Surface: SurfaceKind(RuntimeOperator), RuntimeKind: RuntimeOperator, Name: name + unrepliableSuffix, Status: StatusIdle}
}

// IsUnrepliableSender reports whether id names an unrepliable stand-in.
func IsUnrepliableSender(id string) bool { return strings.HasPrefix(id, unrepliablePrefix) }

// NoReplyRouteError refuses a message to one of Agenthail's own
// pseudo-sessions: no model reads them, so nothing sent there is delivered.
// Host and Sender name the original sender an unrepliable stand-in speaks
// for, so the caller can send to it directly.
type NoReplyRouteError struct {
	SessionID string
	Name      string
	Host      string
	Sender    string
}

func (e NoReplyRouteError) Error() string {
	if e.Host == "" {
		return fmt.Sprintf("not delivered: %s is the Agenthail operator, which sends messages that have no session behind them (from the CLI, the dashboard or a script), and no session reads it. Send to the session that should act on this directly.", e.SessionID)
	}
	ref := e.Host + "::" + e.Sender
	return fmt.Sprintf("not delivered: %s has no reply route and no session reads it. It stands in for %s, session %s on host %s, which sent here without a route back. Send to the original sender directly: agenthail send %s \"<message>\" (this works from a machine paired with that host).", e.SessionID, e.Name, e.Sender, e.Host, ref)
}

// NoReplyRoute reports why a message to the session cannot be delivered,
// or nil when the session is not one of Agenthail's own pseudo-sessions.
func NoReplyRoute(session *Session) error {
	if session == nil || session.RuntimeKind != RuntimeOperator {
		return nil
	}
	result := NoReplyRouteError{SessionID: session.ID}
	if rest, ok := strings.CutPrefix(session.ID, unrepliablePrefix); ok {
		if host, sender, ok := strings.Cut(rest, ":"); ok && host != "" && sender != "" {
			result.Host, result.Sender = host, sender
			result.Name = strings.TrimSuffix(session.Name, unrepliableSuffix)
		}
	}
	return result
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

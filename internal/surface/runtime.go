package surface

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// RuntimeKind is what currently carries a session's conversation. A surface
// can have several (a Claude conversation runs in a CLI process or in the
// Claude host); the adapter that serves a session is chosen by its runtime
// kind, never by its surface alone.
type RuntimeKind string

const (
	RuntimeClaudeCLI  RuntimeKind = "claude-cli"
	RuntimeClaudeHost RuntimeKind = "claude-host"
	// RuntimeClaudeBackground is a Claude Code background job (claude --bg),
	// which Claude's own supervisor runs and reports in claude agents.
	RuntimeClaudeBackground RuntimeKind = "claude-bg"
	RuntimeCodexDesktop     RuntimeKind = "codex-desktop"
	RuntimeCodexAppServer   RuntimeKind = "codex-app-server"
	RuntimeNotionWeb        RuntimeKind = "notion-web"
	// RuntimeOperator marks Agenthail's own pseudo-sessions (the person, the
	// Agenthail CLI and unrepliable senders), which no adapter serves.
	RuntimeOperator RuntimeKind = "agenthail"
)

const (
	// PersonSessionID sends what the person who runs this Agenthail wrote in
	// any of their apps when no session of theirs sent it.
	PersonSessionID = "agenthail-person"
	// CLISessionID sends what agenthail send, a script or an API call sent
	// from outside any session. A terminal is not proof of a person.
	CLISessionID = "agenthail-cli"
)

// SourcelessSender is who sent a message that has no source session.
func SourcelessSender(author Author) string {
	if author == AuthorPerson {
		return PersonSessionID
	}
	return CLISessionID
}

func (o SendOptions) Sender() string {
	if o.SourceSessionID != "" {
		return o.SourceSessionID
	}
	return SourcelessSender(o.Author)
}

// AgenthailSender returns the pseudo-session for the person or the Agenthail
// CLI, or false for any other id. personName names the person.
func AgenthailSender(id, personName string) (Session, bool) {
	switch id {
	case PersonSessionID:
		return Session{ID: id, Surface: "person", RuntimeKind: RuntimeOperator, Name: personName, Status: StatusIdle}, true
	case CLISessionID:
		return Session{ID: id, Surface: "cli", RuntimeKind: RuntimeOperator, Name: "Agenthail CLI", Status: StatusIdle}, true
	}
	return Session{}, false
}

// CLILabel marks a message the Agenthail CLI sent as a turn, so the session
// does not take a script's words for the person's.
const CLILabel = "[from Agenthail CLI, no reply route] "

func PeerUUID(kind SurfaceKind, id string) string {
	return uuid.NewSHA1(uuid.NameSpaceURL, []byte(string(kind)+":"+id)).String()
}

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

// UnrepliableSenderOf names the host and session an unrepliable stand-in
// speaks for.
func UnrepliableSenderOf(id string) (hostID, sessionID string, ok bool) {
	rest, ok := strings.CutPrefix(id, unrepliablePrefix)
	if !ok {
		return "", "", false
	}
	return strings.Cut(rest, ":")
}

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
	switch {
	case e.SessionID == PersonSessionID:
		return fmt.Sprintf("not delivered: %s is the person, who reads this session. Put your answer in your reply.", e.SessionID)
	case e.Host == "":
		return fmt.Sprintf("not delivered: %s is the Agenthail CLI, which sends messages from outside any session (agenthail send, a script or an API call), and no session reads it. Send to the session that should act on this directly.", e.SessionID)
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

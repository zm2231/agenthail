package daemon

import (
	"github.com/zm2231/agenthail/internal/surface"
)

// attributeCodexInput names who wrote a user message Codex attributed to a
// client: the person, or the agent or Agenthail that sent it under that id. A
// message no Agenthail send recorded was typed into Codex itself, unless it
// predates this host recording every send to the session; such a message
// keeps no origin unless its id names an agent.
func (s *sessionSource) attributeCodexInput(event *surface.StreamEvent) error {
	if s.session.Surface != surface.KindCodex || event.Kind != "message" || event.Role != "user" || event.Origin == "" && event.MessageID == "" {
		return nil
	}
	if event.Origin != "" && event.Origin != surface.OriginPerson {
		return nil
	}
	sender, found := "", false
	if event.MessageID != "" {
		var err error
		if sender, found, err = s.manager.registry.MessageSender(event.MessageID); err != nil {
			return err
		}
	}
	if found && sender != surface.PersonSessionID {
		event.Origin = surface.AgentKindPeer
		return nil
	}
	since, err := s.manager.registry.MessageSendersSince(s.session.ID)
	if err != nil {
		return err
	}
	if event.Timestamp.IsZero() || event.Timestamp.Before(since) {
		event.Origin = ""
		return nil
	}
	event.Origin = surface.OriginPerson
	return nil
}

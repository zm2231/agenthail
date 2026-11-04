package coordinator

import (
	"context"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type Route struct {
	Replaced bool
	Moved    bool
	Current  string
	Reason   string
}

func FromAgent(author surface.Author, sourceSessionID string) bool {
	if author == surface.AuthorPerson || sourceSessionID == "" {
		return false
	}
	_, agenthail := surface.AgenthailSender(sourceSessionID, "")
	return !agenthail
}

// An agent addressing a replaced lead is most likely replying on a stale
// route, so its message moves to the current lead. The person's stays with
// the conversation they addressed unless that is gone.
func (s Service) RouteOf(ctx context.Context, sessionID string, fromAgent bool) (Route, error) {
	current, replaced := s.ReplacedBy(sessionID)
	if !replaced {
		return Route{}, nil
	}
	route := Route{Replaced: true, Current: current}
	if fromAgent {
		route.Moved, route.Reason = true, fmt.Sprintf("conversation %s was replaced as the coordinator", sessionID)
		return route, nil
	}
	session, err := s.Registry.Session(sessionID)
	if err != nil {
		return route, fmt.Errorf("read replaced coordinator session %s: %w", sessionID, err)
	}
	adapter, err := s.Adapters.For(session)
	if err != nil {
		return route, err
	}
	keeper, ok := adapter.(surface.ConversationKeeper)
	if !ok {
		return route, nil
	}
	state, since, err := s.conversationState(ctx, keeper, session)
	switch state {
	case surface.ConversationGone:
		route.Moved, route.Reason = true, fmt.Sprintf("conversation %s no longer exists (nothing holds it and it was last seen %s ago)", sessionID, since.Round(time.Second))
	case surface.ConversationUnknown:
		return route, surface.DeliveryUnavailable(fmt.Errorf("check conversation %s: %w", sessionID, err))
	}
	return route, nil
}

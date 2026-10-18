package delivery

import (
	"context"
	"errors"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

// Route is the transport a message takes. It is derived from the target and
// the message's author each time the message is sent, never stored, because a
// queued message's target can gain or lose its web bridge while it waits.
type Route int

const (
	RouteSend Route = iota
	RoutePeer
	RouteHumanTurn
)

func RouteFor(adapter surface.Surface, session *surface.Session, author surface.Author) Route {
	if sender, ok := adapter.(surface.HumanTurnSender); ok && author == surface.AuthorPerson && sender.CanSendHumanTurn(session) {
		return RouteHumanTurn
	}
	if session.Surface == surface.KindClaude && session.Transport == "uds" {
		return RoutePeer
	}
	return RouteSend
}

func (r Route) TransportOnly() bool { return r == RoutePeer }

func (r Route) Validate(adapter surface.Surface, options surface.SendOptions) error {
	if options.Model == "" && options.TurnOptions.Empty() {
		return nil
	}
	if r == RouteHumanTurn {
		return surface.DeliveryTerminal(errors.New("per-message model and turn options are not supported for a person's Claude turn"), surface.DeliveryInvalidRequest)
	}
	if _, ok := adapter.(surface.OptionSender); !ok {
		return fmt.Errorf("%s does not support per-message model selection", adapter.Name())
	}
	return nil
}

// Send must follow a successful Validate.
func (r Route) Send(ctx context.Context, adapter surface.Surface, session *surface.Session, message string, options surface.SendOptions) (*surface.SendResult, error) {
	if r == RouteHumanTurn {
		return adapter.(surface.HumanTurnSender).SendHumanTurn(ctx, session, message)
	}
	if options.Model != "" || !options.TurnOptions.Empty() {
		return adapter.(surface.OptionSender).SendWithOptions(ctx, session, message, options)
	}
	return adapter.Send(ctx, session, message)
}

package surface

import (
	"context"
	"errors"
	"fmt"
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

// Router is implemented by an adapter whose sessions take more than one
// route. Every other adapter's messages take RouteSend.
type Router interface {
	Route(session *Session, author Author) Route
}

func RouteFor(adapter Surface, session *Session, author Author) Route {
	if router, ok := adapter.(Router); ok {
		return router.Route(session, author)
	}
	return RouteSend
}

func (r Route) TransportOnly() bool { return r == RoutePeer }

func (r Route) Validate(adapter Surface, options SendOptions) error {
	if len(options.Uploads) > 0 && !adapter.Capabilities().Attach {
		return DeliveryTerminal(fmt.Errorf("%s sessions cannot take attached files", adapter.Name()), DeliveryInvalidRequest)
	}
	if options.Model == "" && options.TurnOptions.Empty() {
		return nil
	}
	if r == RouteHumanTurn {
		return DeliveryTerminal(errors.New("per-message model and turn options are not supported for a person's Claude turn"), DeliveryInvalidRequest)
	}
	if _, ok := adapter.(OptionSender); !ok {
		return fmt.Errorf("%s does not support per-message model selection", adapter.Name())
	}
	return nil
}

// Send must follow a successful Validate.
func (r Route) Send(ctx context.Context, adapter Surface, session *Session, message string, options SendOptions) (*SendResult, error) {
	router, native := adapter.(ImageRouter)
	message, options.Images = options.Uploads.Compose(message, native && router.NativeImages(session, r))
	if r == RouteHumanTurn {
		return adapter.(HumanTurnSender).SendHumanTurn(ctx, session, message, options.Images)
	}
	if options.Model != "" || !options.TurnOptions.Empty() || len(options.Images) > 0 {
		return adapter.(OptionSender).SendWithOptions(ctx, session, message, options)
	}
	return adapter.Send(ctx, session, message)
}

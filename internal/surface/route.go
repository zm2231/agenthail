package surface

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// Route is the transport a message takes. It is derived from the target and
// the message's author each time the message is sent, never stored, because a
// queued message's target can gain or lose its web bridge while it waits.
type Route int

const (
	RouteSend Route = iota
	RoutePeer
	RouteHumanTurn
	RouteAgenthailTurn
)

func (r Route) String() string {
	switch r {
	case RoutePeer:
		return "peer"
	case RouteHumanTurn:
		return "human-turn"
	case RouteAgenthailTurn:
		return "agenthail-turn"
	default:
		return "send"
	}
}

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

// PersonMessage is how a message on this route reaches the session: a peer socket hands the model a note.
func (r Route) PersonMessage() PersonMessage {
	if r == RoutePeer {
		return PersonNote
	}
	return PersonTurn
}

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
	if r == RouteAgenthailTurn {
		return DeliveryTerminal(errors.New("per-message model and turn options are not supported for Agenthail's own Claude turn"), DeliveryInvalidRequest)
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
	if _, routed := adapter.(Router); !routed {
		message = LabelCLI(ctx, message)
	}
	if r == RouteHumanTurn {
		return adapter.(HumanTurnSender).SendHumanTurn(ctx, session, message, options.Images)
	}
	if r == RouteAgenthailTurn {
		return adapter.(AgenthailTurnSender).SendAgenthailTurn(ctx, session, message)
	}
	if options.Model != "" || !options.TurnOptions.Empty() || len(options.Images) > 0 {
		return adapter.(OptionSender).SendWithOptions(ctx, session, message, options)
	}
	return adapter.Send(ctx, session, message)
}

// LabelCLI marks a message the Agenthail CLI sends as a turn. An adapter with
// more than one route labels it where it knows the transport, because the
// peer route names its sender in its own envelope. A channel post already
// names its sender.
func LabelCLI(ctx context.Context, message string) string {
	if SourceSessionID(ctx) == CLISessionID && message != "" && !strings.HasPrefix(message, "[from ") {
		return CLILabel + message
	}
	return message
}

// The label comes from who sent the message, never from its text.
func LabelSender(ctx context.Context, message string) string {
	source := SourceSessionID(ctx)
	if _, agenthail := AgenthailSender(source, ""); agenthail || source == "" || message == "" {
		return LabelCLI(ctx, message)
	}
	name := ReplyTarget(ctx)
	if sender, ok := Sender(ctx); name == "" && ok {
		name = sender.Name
	}
	if name = strings.Map(func(r rune) rune {
		if r == ']' || r == '[' || r == '\n' || r == '\r' {
			return -1
		}
		return r
	}, strings.TrimSpace(name)); name == "" {
		name = source
	}
	if strings.HasPrefix(message, "[from "+name+"] ") || strings.HasPrefix(message, "[from "+name+channelSeparator) {
		return message
	}
	return "[from " + name + "] " + message
}

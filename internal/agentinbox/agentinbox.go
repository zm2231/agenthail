// Package agentinbox serves agent identities: senders outside Agenthail whose messages wait in an inbox.
package agentinbox

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type Inbox struct {
	registry func() *registry.Registry
}

func New(registry func() *registry.Registry) *Inbox { return &Inbox{registry: registry} }

func (*Inbox) Unlisted() {}

func (*Inbox) Name() surface.SurfaceKind { return surface.KindAgent }

func (*Inbox) RuntimeKinds() []surface.RuntimeKind {
	return []surface.RuntimeKind{surface.RuntimeAgentInbox}
}

func (*Inbox) Route(*surface.Session, surface.Author) surface.Route { return surface.RouteInbox }

func (*Inbox) Capabilities() surface.Capabilities {
	return surface.Capabilities{Send: true, PersonMessage: surface.PersonNote}
}

func (i *Inbox) store() (*registry.Registry, error) {
	if i.registry == nil {
		return nil, errors.New("agent identities need the Agenthail registry")
	}
	reg := i.registry()
	if reg == nil {
		return nil, errors.New("agent identities need the Agenthail registry")
	}
	return reg, nil
}

func (i *Inbox) List(context.Context) ([]surface.Session, error) {
	reg, err := i.store()
	if err != nil {
		return nil, err
	}
	sessions, err := reg.AgentIdentitySessions()
	if err != nil {
		return nil, err
	}
	for index := range sessions {
		sessions[index].Status = surface.StatusIdle
		sessions[index].Ended = false
		sessions[index].Runtime = &surface.Runtime{Launcher: surface.LauncherExternal}
	}
	return sessions, nil
}

func (i *Inbox) Resolve(_ context.Context, target string) (*surface.Session, error) {
	reg, err := i.store()
	if err != nil {
		return nil, err
	}
	// A surface-qualified selector reaches here without its "agent:", which is also how identity ids begin.
	for _, id := range []string{target, string(surface.KindAgent) + ":" + target} {
		if identity, err := reg.AgentIdentityOf(id); err == nil {
			return reg.Session(identity.SessionID)
		}
	}
	name := strings.TrimPrefix(strings.TrimPrefix(target, string(surface.KindAgent)+":"), "@")
	identity, err := reg.AgentIdentityNamed(name)
	if err != nil {
		return nil, err
	}
	return reg.Session(identity.SessionID)
}

func (i *Inbox) Observe(context.Context, *surface.Session) (*surface.TurnObservation, error) {
	return &surface.TurnObservation{Status: surface.StatusIdle}, nil
}

func (i *Inbox) Send(ctx context.Context, session *surface.Session, message string) (*surface.SendResult, error) {
	reg, err := i.store()
	if err != nil {
		return nil, err
	}
	source := surface.SourceSessionID(ctx)
	stored, _, err := reg.StoreInboxMessage(registry.InboxMessage{Identity: session.ID, UUID: surface.MessageUUID(ctx), SenderID: source, SenderName: senderName(ctx, reg, source), Message: message, ReceivedAt: time.Now()})
	if err != nil {
		return nil, surface.DeliveryTerminal(err, surface.DeliveryInvalidRequest)
	}
	id := stored.UUID
	if id == "" {
		id = "inbox-" + strconv.FormatInt(stored.ID, 10)
	}
	return &surface.SendResult{UUID: id, Accepted: true}, nil
}

// A reply address where one exists, so the inbox reader can answer with agenthail send.
func senderName(ctx context.Context, reg *registry.Registry, source string) string {
	if target := surface.ReplyTarget(ctx); target != "" {
		return target
	}
	if source == surface.PersonSessionID {
		if name := surface.PersonName(ctx); name != "" {
			return name
		}
	}
	if sender, ok := surface.AgenthailSender(source, surface.PersonName(ctx)); ok {
		return sender.Name
	}
	if session, err := reg.Session(source); err == nil && session.RuntimeKind != surface.RuntimeOperator {
		owner, _ := reg.SessionHost(source)
		if owner == "" {
			return source
		}
		if host, err := reg.Host(owner); err == nil {
			return hosts.Qualify(host.Name, source)
		}
	}
	if sender, ok := surface.Sender(ctx); ok && sender.Name != "" {
		return sender.Name
	}
	return source
}

func noReply(session *surface.Session) error {
	return fmt.Errorf("%s is an agent identity: messages to it wait in its inbox and nothing replies here; its agent reads them with 'agenthail inbox %s'", session.Name, session.Name)
}

func (i *Inbox) Reply(_ context.Context, session *surface.Session, _ int) (*surface.ReplyResult, error) {
	return nil, noReply(session)
}

func (i *Inbox) Tail(_ context.Context, session *surface.Session, n int) ([]surface.Exchange, error) {
	reg, err := i.store()
	if err != nil {
		return nil, err
	}
	identity, err := reg.AgentIdentityOf(session.ID)
	if err != nil {
		return nil, err
	}
	messages, err := reg.ReadInbox(identity.Name, registry.InboxQuery{All: true, Peek: true})
	if err != nil {
		return nil, err
	}
	if n > 0 && len(messages) > n {
		messages = messages[len(messages)-n:]
	}
	exchanges := make([]surface.Exchange, 0, len(messages))
	for _, message := range messages {
		exchanges = append(exchanges, surface.Exchange{User: message.Message, Origin: "peer", Sender: message.SenderName, Timestamp: message.ReceivedAt})
	}
	return exchanges, nil
}

func (i *Inbox) Stream(_ context.Context, session *surface.Session, _ string, _ func(surface.StreamEvent), _ time.Duration) error {
	return noReply(session)
}

func unsupported(session *surface.Session, what string) error {
	return surface.DeliveryTerminal(fmt.Errorf("%s is an agent identity, which has no %s", session.Name, what), surface.DeliveryInvalidRequest)
}

func (i *Inbox) GoalSet(_ context.Context, session *surface.Session, _ string) error {
	return unsupported(session, "goal")
}

func (i *Inbox) GoalClear(_ context.Context, session *surface.Session) error {
	return unsupported(session, "goal")
}

func (i *Inbox) GoalGet(_ context.Context, session *surface.Session) (*surface.GoalState, error) {
	return nil, unsupported(session, "goal")
}

func (i *Inbox) Compact(_ context.Context, session *surface.Session) error {
	return unsupported(session, "conversation to compact")
}

func (i *Inbox) Model(_ context.Context, session *surface.Session, _ string) (string, error) {
	return "", unsupported(session, "model")
}

func (i *Inbox) Interrupt(_ context.Context, session *surface.Session) error {
	return unsupported(session, "turn to interrupt")
}

func (i *Inbox) Steer(_ context.Context, session *surface.Session, _ string) error {
	return unsupported(session, "turn to steer")
}

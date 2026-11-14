package cli

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

const joinedRunningTurnNote = "joined a running turn; this reply ends that turn"

var errTerminalPeerTurns = errors.New("a Claude session in a terminal takes this message through its socket, and its turns cannot be followed; use 'agenthail last' or native SendMessage replies")

func hostRunsClaude(sess *surface.Session) bool {
	return sess != nil && sess.Surface == surface.KindClaude && sess.RuntimeKind == surface.RuntimeClaudeHost
}

// turnFollow picks the stream events of the turn a send started. A message a
// Claude socket took starts its own turn when the target is idle and joins the
// running one when it is busy; the transcript names it by its delivery uuid in
// both cases, and only that names the turn: no other turn is ever adopted.
type turnFollow struct {
	turnID    string
	messageID string
	joined    bool
	settled   func() error
}

func followTurn(turnID string) *turnFollow {
	return &turnFollow{turnID: turnID}
}

func followMessage(messageID string, settled func() error) *turnFollow {
	return &turnFollow{messageID: messageID, settled: settled}
}

func (f *turnFollow) takes(event sessionstream.Event) bool {
	if f.turnID == "" && event.TurnID != "" && (event.MessageID == f.messageID || event.TurnID == f.messageID) {
		f.turnID, f.joined = event.TurnID, !event.TurnStart && event.TurnID != f.messageID
	}
	return f.turnID != "" && event.TurnID == f.turnID
}

func (f *turnFollow) waiting() bool {
	return f.turnID == "" && f.settled != nil
}

func (f *turnFollow) label() string {
	if f.turnID != "" {
		return "turn " + f.turnID
	}
	return "the turn that took message " + f.messageID
}

func (f *turnFollow) timedOut(err error) error {
	if f.turnID == "" {
		return fmt.Errorf("could not find the turn that took message %s; read the session with 'agenthail last': %w", f.messageID, err)
	}
	return err
}

func deliverySettled(reg *registry.Registry, deliveryID int64) func() error {
	if reg == nil || deliveryID == 0 {
		return nil
	}
	return func() error {
		intent, err := reg.DeliveryIntent(deliveryID)
		if err != nil {
			return nil
		}
		switch intent.Status {
		case registry.DeliveryIntentHeld:
			return fmt.Errorf("the target holds this message and started no turn: %s", intent.Failure)
		case registry.DeliveryIntentFailed:
			return fmt.Errorf("the message was not delivered: %s", intent.Failure)
		}
		return nil
	}
}

const turnFollowCheck = time.Second

func settleTicker(f *turnFollow) (<-chan time.Time, func()) {
	if !f.waiting() {
		return nil, func() {}
	}
	ticker := time.NewTicker(turnFollowCheck)
	return ticker.C, ticker.Stop
}

func checkSettled(ctx context.Context, f *turnFollow) error {
	if !f.waiting() || ctx.Err() != nil {
		return nil
	}
	return f.settled()
}

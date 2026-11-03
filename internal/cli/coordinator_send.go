package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/sessionstream"
	"github.com/zm2231/agenthail/internal/surface"
)

type coordinatorSendAnswer struct {
	Result  delivery.Receipt `json:"result"`
	Started bool             `json:"started"`
}

// coordinatorSend hands a message for a coordinator to the daemon, the one
// process that decides whether its lead is gone and starts a new one.
func (a *App) coordinatorSend(ctx context.Context, spec registry.CoordinatorSpec, message string, options surface.SendOptions, noQueue bool) (coordinatorSendAnswer, error) {
	var answer coordinatorSendAnswer
	client, err := a.daemonAPI()
	if err != nil {
		return answer, fmt.Errorf("a coordinator is reached through the daemon: %w", err)
	}
	payload := map[string]any{}
	if !options.TurnOptions.Empty() {
		data, err := json.Marshal(options.TurnOptions)
		if err != nil {
			return answer, err
		}
		if err := json.Unmarshal(data, &payload); err != nil {
			return answer, err
		}
	}
	payload["action"], payload["name"], payload["message"] = "coordinator-send", spec.Name, message
	if spec.HostID != "" {
		payload["host"] = spec.HostID
	}
	if options.SourceSessionID != "" {
		payload["sourceSessionId"] = options.SourceSessionID
	}
	if options.Model != "" {
		payload["model"] = options.Model
	}
	if noQueue {
		payload["noQueue"] = true
	}
	data, status, err := client.action(ctx, payload)
	if err != nil {
		return answer, err
	}
	if status != http.StatusOK {
		return answer, actionError(data)
	}
	return answer, json.Unmarshal(data, &answer)
}

func (a *App) receiptSession(receipt delivery.Receipt, addressed surface.Session) *surface.Session {
	if receipt.SessionID == "" || receipt.SessionID == addressed.ID {
		return &addressed
	}
	session := surface.Session{ID: receipt.SessionID, Surface: addressed.Surface}
	if a.registry() != nil {
		if stored, err := a.registry().Session(receipt.SessionID); err == nil {
			session = *stored
		}
	}
	return &session
}

// followReceipt moves a send that follows its turn onto the conversation its
// receipt names. A conversation started for the message is followed from the
// start of its journal; any other move cancels following and returns the note
// to tell the sender. The superseded stream is cancelled either way.
func (a *App) followReceipt(ctx context.Context, receipt delivery.Receipt, addressed surface.Session, stream *sessionstream.Subscription) (*surface.Session, string) {
	if receipt.SessionID == "" || receipt.SessionID == addressed.ID {
		return &addressed, ""
	}
	if stream.Cancel != nil {
		stream.Cancel()
	}
	*stream = sessionstream.Subscription{}
	session := *a.receiptSession(receipt, addressed)
	if !receipt.Started {
		return &session, fmt.Sprintf("the coordinator moved to %s while this message was sent, so this send cannot follow its turns; read the reply with 'agenthail last %s'", session.ID, session.ID)
	}
	var err error = errors.New("its first turn was submitted but not confirmed")
	if receipt.TurnID != "" {
		var next sessionstream.Subscription
		if next, err = a.openDaemonSessionStream(ctx, &session, 0); err == nil {
			*stream = next
			return &session, ""
		}
	}
	return &session, fmt.Sprintf("the coordinator started a new conversation %s for this message, but this send cannot follow it (%s); read the reply with 'agenthail last %s'", session.ID, err, session.ID)
}

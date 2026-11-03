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

// followStartedCoordinator opens the stream of the conversation the daemon
// started for a send, so a send that asked for its reply follows the new lead.
func (a *App) followStartedCoordinator(ctx context.Context, receipt delivery.Receipt, spec registry.CoordinatorSpec) (*surface.Session, sessionstream.Subscription, error) {
	if receipt.SessionID == "" || receipt.TurnID == "" {
		return nil, sessionstream.Subscription{}, errors.New("its first turn was submitted but not confirmed")
	}
	session := surface.Session{ID: receipt.SessionID, Surface: surface.SurfaceKind(spec.Surface)}
	if stored, err := a.registry().Session(receipt.SessionID); err == nil {
		session = *stored
	}
	stream, err := a.openDaemonSessionStream(ctx, &session, 0)
	if err != nil {
		return nil, sessionstream.Subscription{}, err
	}
	return &session, stream, nil
}

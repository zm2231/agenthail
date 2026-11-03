package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/zm2231/agenthail/internal/delivery"
	"github.com/zm2231/agenthail/internal/registry"
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

package surfaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func applyCodexTurnOptions(ctx context.Context, client codexClient, id, model string, options surface.TurnOptions, params map[string]any) error {
	if err := options.Validate(surface.KindCodex); err != nil {
		return err
	}
	if model != "" {
		params["model"] = model
	}
	if options.Effort != "" {
		params["effort"] = options.Effort
	}
	if options.ServiceTier != "" {
		params["serviceTierForTurn"] = options.ServiceTier
	}
	if len(options.OutputSchema) > 0 {
		params["outputSchema"] = options.OutputSchema
	}
	if options.Mode != "" {
		if model == "" {
			response, err := client.Request(ctx, "thread/resume", map[string]any{"threadId": id}, 5*time.Second)
			if err != nil {
				return err
			}
			result, _ := response["result"].(map[string]any)
			model = str(result, "model")
			if model == "" {
				return fmt.Errorf("cannot select collaboration mode without the session model")
			}
		}
		var effort any
		if options.Effort != "" {
			effort = options.Effort
		}
		params["collaborationMode"] = map[string]any{"mode": options.Mode, "settings": map[string]any{"model": model, "reasoning_effort": effort, "developer_instructions": nil}}
	}
	return nil
}

func (c *Codex) ForkSession(ctx context.Context, session *surface.Session, options surface.ForkOptions) (*surface.Session, error) {
	if options.BeforeTurnID != "" && options.LastTurnID != "" {
		return nil, fmt.Errorf("before-turn and last-turn are mutually exclusive")
	}
	params := map[string]any{"threadId": session.ID, "deferGoalContinuation": true, "excludeTurns": true, "threadSource": "agenthail"}
	for k, v := range map[string]string{"cwd": options.Cwd, "model": options.Model, "beforeTurnId": options.BeforeTurnID, "lastTurnId": options.LastTurnID} {
		if v != "" {
			params[k] = v
		}
	}
	response, err := c.requestSession(ctx, session, true, "thread/fork", params, 15*time.Second)
	if err != nil {
		if surface.IsDeliveryUnavailable(err) {
			return nil, err
		}
		return nil, surface.DeliveryOutcomeUnknown(err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	id := str(thread, "id")
	if id == "" {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("fork returned no thread id"))
	}
	fork := *session
	fork.ID = id
	fork.PID = 0
	fork.Transcript = ""
	fork.Status = surface.StatusIdle
	fork.Name = str(thread, "name")
	fork.Cwd = str(result, "cwd")
	if source := codexSource(thread["source"]); source != "" {
		fork.Source = source
	}
	if fork.Cwd == "" {
		fork.Cwd = str(thread, "cwd")
	}
	if fork.Cwd == "" {
		fork.Cwd = options.Cwd
	}
	fork.LastActive = time.Now()
	if fork.Cwd == "" {
		fork.Cwd = session.Cwd
	}
	return &fork, nil
}

func nativeQueueParams(session *surface.Session, request surface.NativeQueueRequest) (string, map[string]any, error) {
	params := map[string]any{"threadId": session.ID}
	switch request.Action {
	case "list":
		if request.Cursor != "" {
			params["cursor"] = request.Cursor
		}
		params["limit"] = 100
	case "add", "update":
		if strings.TrimSpace(request.Message) == "" {
			return "", nil, fmt.Errorf("message is required")
		}
		params["input"] = []map[string]any{{"type": "text", "text": request.Message}}
		if request.Action == "add" {
			if request.ClientID == "" {
				return "", nil, fmt.Errorf("clientUserMessageId is required for retry-safe native enqueue")
			}
			params["clientUserMessageId"] = request.ClientID
		} else {
			if request.ID == "" {
				return "", nil, fmt.Errorf("queuedSubmissionId is required")
			}
			params["queuedSubmissionId"] = request.ID
		}
	case "delete":
		if request.ID == "" {
			return "", nil, fmt.Errorf("queuedSubmissionId is required")
		}
		params["queuedSubmissionId"] = request.ID
	case "reorder":
		if len(request.IDs) == 0 {
			return "", nil, fmt.Errorf("queuedSubmissionIds are required")
		}
		params["queuedSubmissionIds"] = request.IDs
	case "start":
		if request.ID != "" {
			params["queuedSubmissionId"] = request.ID
		}
	default:
		return "", nil, fmt.Errorf("native queue action must be list, add, update, delete, reorder, or start")
	}
	return "thread/queue/" + request.Action, params, nil
}

func (c *Codex) NativeQueue(ctx context.Context, session *surface.Session, request surface.NativeQueueRequest) (map[string]any, error) {
	method, params, err := nativeQueueParams(session, request)
	if err != nil {
		return nil, err
	}
	response, err := c.requestSession(ctx, session, request.Action != "list", method, params, 10*time.Second)
	if err != nil {
		if request.Action != "list" {
			if surface.IsDeliveryUnavailable(err) {
				return nil, err
			}
			return nil, surface.DeliveryOutcomeUnknown(err)
		}
		return nil, err
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return nil, surface.DeliveryOutcomeUnknown(fmt.Errorf("native queue returned malformed result"))
	}
	return result, nil
}

package surfaces

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexTurn struct {
	ID        string
	Status    surface.SessionStatus
	User      string
	Assistant string
	Done      bool
	Error     string
}

type codexThread struct {
	ID     string
	Name   string
	Cwd    string
	Status surface.SessionStatus
	Turns  []codexTurn
}

func (c *Codex) readThread(ctx context.Context, conn *cdpConn, threadID string) (*codexThread, error) {
	response, err := c.rpc(ctx, conn, "thread/read", map[string]any{
		"threadId":     threadID,
		"includeTurns": true,
	}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/read: %w", err)
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("thread/read response missing result")
	}
	value, ok := result["thread"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("thread/read response missing thread")
	}
	thread := &codexThread{
		ID:     str(value, "id"),
		Name:   surface.DeriveName(str(value, "name"), str(value, "preview"), 60),
		Cwd:    str(value, "cwd"),
		Status: codexStatus(value["status"]),
	}
	if thread.ID == "" {
		thread.ID = threadID
	}
	turns, _ := value["turns"].([]any)
	for _, rawTurn := range turns {
		entry, _ := rawTurn.(map[string]any)
		status, done, turnError := codexTurnState(entry["status"])
		turn := codexTurn{ID: str(entry, "id"), Status: status, Done: done, Error: turnError}
		items, _ := entry["items"].([]any)
		for _, rawItem := range items {
			item, _ := rawItem.(map[string]any)
			switch item["type"] {
			case "userMessage", "user":
				turn.User = codexItemText(item)
			case "agentMessage", "assistant":
				if text, _ := item["text"].(string); text != "" {
					turn.Assistant = text
				}
			}
		}
		thread.Turns = append(thread.Turns, turn)
	}
	return thread, nil
}

func codexTurnStatus(value any) surface.SessionStatus {
	status, _, _ := codexTurnState(value)
	return status
}

func codexTurnState(value any) (surface.SessionStatus, bool, string) {
	if object, ok := value.(map[string]any); ok {
		value = object["type"]
	}
	status, _ := value.(string)
	normalized := strings.ToLower(strings.ReplaceAll(status, "_", ""))
	switch normalized {
	case "running", "inprogress", "active", "queued":
		return surface.StatusBusy, false, ""
	case "completed", "complete", "succeeded", "success":
		return surface.StatusIdle, true, ""
	case "interrupted", "cancelled", "canceled", "failed", "error", "systemerror":
		return surface.StatusIdle, true, "turn " + normalized
	case "":
		return surface.StatusUnknown, false, ""
	default:
		return surface.SessionStatus(status), false, ""
	}
}

func codexItemText(item map[string]any) string {
	if text, _ := item["text"].(string); text != "" {
		return text
	}
	content, _ := item["content"].([]any)
	var parts []string
	for _, raw := range content {
		entry, _ := raw.(map[string]any)
		if text, _ := entry["text"].(string); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func codexObservation(thread *codexThread) *surface.TurnObservation {
	observation := &surface.TurnObservation{Status: thread.Status}
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		turn := thread.Turns[i]
		if turn.Status == surface.StatusBusy {
			observation.Status = surface.StatusBusy
			observation.ActiveTurnID = turn.ID
			break
		}
	}
	for i := len(thread.Turns) - 1; i >= 0; i-- {
		turn := thread.Turns[i]
		if turn.ID == "" || !turn.Done {
			continue
		}
		if observation.TerminalTurnID == "" {
			observation.TerminalTurnID = turn.ID
		}
		if turn.Assistant == "" && turn.Error == "" {
			continue
		}
		observation.CompletedTurnID = turn.ID
		observation.Reply = &surface.ReplyResult{Text: turn.Assistant, UserText: turn.User, Done: true, Error: turn.Error}
		break
	}
	return observation
}

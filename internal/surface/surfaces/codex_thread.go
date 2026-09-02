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

func (c *Codex) readThread(ctx context.Context, conn codexClient, threadID string) (*codexThread, error) {
	return c.readThreadWithOptions(ctx, conn, threadID, 50, true)
}

func (c *Codex) readObservationThread(ctx context.Context, conn codexClient, threadID string) (*codexThread, error) {
	return c.readThreadWithOptions(ctx, conn, threadID, 3, false)
}

func (c *Codex) readThreadWithOptions(ctx context.Context, conn codexClient, threadID string, turnLimit int, hydrateAll bool) (*codexThread, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{
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
	if len(turns) == 0 {
		page, pageErr := c.listThreadTurns(ctx, conn, thread.ID, turnLimit)
		if pageErr != nil {
			return nil, pageErr
		}
		turns = page
	}
	hydrate := codexHydrationCandidates(turns, hydrateAll)
	for index, rawTurn := range turns {
		entry, _ := rawTurn.(map[string]any)
		status, done, turnError := codexTurnState(entry["status"])
		turn := codexTurn{ID: str(entry, "id"), Status: status, Done: done, Error: turnError}
		items, _ := entry["items"].([]any)
		if len(items) == 0 && turn.ID != "" && hydrate[index] {
			page, pageErr := c.listThreadItems(ctx, conn, thread.ID, turn.ID)
			if pageErr != nil {
				return nil, pageErr
			}
			items = page
		}
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

func codexHydrationCandidates(turns []any, hydrateAll bool) map[int]bool {
	candidates := make(map[int]bool, len(turns))
	if hydrateAll {
		for index := range turns {
			candidates[index] = true
		}
		return candidates
	}
	needActive, needTerminal := true, true
	for index := len(turns) - 1; index >= 0 && (needActive || needTerminal); index-- {
		entry, _ := turns[index].(map[string]any)
		status, done, _ := codexTurnState(entry["status"])
		if needActive && status == surface.StatusBusy {
			candidates[index] = true
			needActive = false
		}
		if needTerminal && done {
			candidates[index] = true
			needTerminal = false
		}
	}
	return candidates
}

func (c *Codex) listThreadTurns(ctx context.Context, conn codexClient, threadID string, limit int) ([]any, error) {
	if limit < 1 {
		limit = 1
	}
	response, err := conn.Request(ctx, "thread/turns/list", map[string]any{
		"threadId":      threadID,
		"page":          map[string]any{"limit": limit},
		"sortDirection": "desc",
	}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/turns/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	for left, right := 0, len(turns)-1; left < right; left, right = left+1, right-1 {
		turns[left], turns[right] = turns[right], turns[left]
	}
	return turns, nil
}

func (c *Codex) listThreadItems(ctx context.Context, conn codexClient, threadID, turnID string) ([]any, error) {
	response, err := conn.Request(ctx, "thread/items/list", map[string]any{
		"threadId": threadID,
		"turnId":   turnID,
		"page":     map[string]any{"limit": 100},
	}, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("thread/items/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	items := make([]any, 0, len(values))
	for _, value := range values {
		entry, _ := value.(map[string]any)
		if item, ok := entry["item"]; ok {
			items = append(items, item)
			continue
		}
		items = append(items, value)
	}
	return items, nil
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

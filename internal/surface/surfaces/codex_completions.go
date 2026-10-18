package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) ReadCompletions(ctx context.Context, sess *surface.Session, cursor string) (surface.CompletionBatch, error) {
	conn, err := c.openReader(ctx, sess)
	if err != nil {
		return surface.CompletionBatch{}, err
	}
	defer conn.Close()
	return c.readCompletions(ctx, conn, sess.ID, cursor)
}

func (c *Codex) readCompletions(ctx context.Context, conn codexClient, threadID, cursor string) (surface.CompletionBatch, error) {
	var after string
	if cursor != "" {
		if err := json.Unmarshal([]byte(cursor), &after); err != nil {
			return surface.CompletionBatch{}, err
		}
	}
	var turns []map[string]any
	pageCursor := ""
	seen := map[string]bool{}
	found := after == ""
	boundary := ""
	for {
		params := map[string]any{"threadId": threadID, "limit": 100, "sortDirection": "desc", "itemsView": "notLoaded"}
		if pageCursor != "" {
			params["cursor"] = pageCursor
		}
		response, err := conn.Request(ctx, "thread/turns/list", params, 5*time.Second)
		if err != nil {
			return surface.CompletionBatch{}, err
		}
		result, ok := response["result"].(map[string]any)
		if !ok {
			return surface.CompletionBatch{}, fmt.Errorf("completion page missing result")
		}
		values, ok := result["data"].([]any)
		if !ok {
			return surface.CompletionBatch{}, fmt.Errorf("completion page missing data")
		}
		stop := false
		for _, raw := range values {
			turn, _ := raw.(map[string]any)
			id := str(turn, "id")
			if id == "" {
				return surface.CompletionBatch{}, fmt.Errorf("completion page has a turn without an id")
			}
			if id == after {
				found = true
				stop = true
				break
			}
			_, done, _ := codexTurnState(turn["status"])
			if !done {
				continue
			}
			if boundary == "" {
				boundary = id
			}
			if cursor == "" {
				stop = true
				break
			}
			turns = append(turns, turn)
		}
		next := str(result, "nextCursor")
		if stop || next == "" {
			break
		}
		if seen[next] {
			return surface.CompletionBatch{}, fmt.Errorf("completion pagination repeated its cursor")
		}
		seen[next] = true
		pageCursor = next
	}
	if !found {
		return surface.CompletionBatch{}, fmt.Errorf("saved relay turn %s is missing from provider history", after)
	}
	if boundary == "" {
		boundary = after
	}
	encoded, _ := json.Marshal(boundary)
	batch := surface.CompletionBatch{Cursor: string(encoded)}
	for i := len(turns) - 1; i >= 0; i-- {
		entry := turns[i]
		_, _, failure := codexTurnState(entry["status"])
		if failure != "" {
			continue
		}
		turn := codexTurn{ID: str(entry, "id")}
		items, _ := entry["items"].([]any)
		if len(items) == 0 || str(entry, "itemsView") != "full" {
			var err error
			items, err = c.completionItems(ctx, conn, threadID, turn.ID)
			if err != nil {
				return surface.CompletionBatch{}, err
			}
		}
		for _, raw := range items {
			item, _ := raw.(map[string]any)
			switch item["type"] {
			case "userMessage", "user":
				turn.User = codexItemText(item)
			case "agentMessage", "assistant":
				turn.AssistantItems = append(turn.AssistantItems, codexAssistantItem{ID: str(item, "id"), Phase: str(item, "phase"), Text: codexItemText(item)})
			}
		}
		answer, _ := turn.authoritativeAssistant()
		batch.Events = append(batch.Events, surface.Completion{ID: turn.ID, InputID: turn.ID, Text: answer.Text, User: turn.User, CompletedAt: codexUnixTime(entry["completedAt"])})
	}
	return batch, nil
}

func (c *Codex) completionItems(ctx context.Context, conn codexClient, threadID, turnID string) ([]any, error) {
	var items []any
	cursor := ""
	seen := map[string]bool{}
	for {
		params := map[string]any{"threadId": threadID, "turnId": turnID, "limit": 100, "sortDirection": "asc"}
		if cursor != "" {
			params["cursor"] = cursor
		}
		response, err := conn.Request(ctx, "thread/items/list", params, 5*time.Second)
		if err != nil {
			return nil, err
		}
		result, _ := response["result"].(map[string]any)
		values, ok := result["data"].([]any)
		if !ok {
			return nil, fmt.Errorf("completion items missing data")
		}
		for _, value := range values {
			entry, _ := value.(map[string]any)
			if item, ok := entry["item"]; ok {
				items = append(items, item)
			} else {
				items = append(items, value)
			}
		}
		next := str(result, "nextCursor")
		if next == "" {
			return items, nil
		}
		if seen[next] {
			return nil, fmt.Errorf("completion items repeated their cursor")
		}
		seen[next] = true
		cursor = next
	}
}

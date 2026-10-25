package surfaces

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Codex) Observe(ctx context.Context, sess *surface.Session) (*surface.TurnObservation, error) {
	conn, err := c.openReader(ctx, sess)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	thread, err := c.readObservationThread(ctx, conn, sess.ID)
	if err != nil {
		return nil, err
	}
	observation := codexObservation(thread)
	if observation.Status == surface.StatusBusy {
		// Only thread status carries the pending approval and input flags.
		response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": sess.ID, "includeTurns": false}, 5*time.Second)
		if err != nil {
			return nil, fmt.Errorf("thread/read: %w", err)
		}
		result, _ := response["result"].(map[string]any)
		value, _ := result["thread"].(map[string]any)
		thread.Waiting = codexWaitingKind(value["status"])
		observation = codexObservation(thread)
	}
	if path := codexTranscriptPath(sess); path != "" {
		if info, err := os.Stat(path); err == nil {
			observation.Plan = c.plan(ctx, path, info)
		}
	}
	return observation, nil
}

// activeTurnID returns the thread's running turn, or "" when none runs. A new
// thread whose turns cannot be listed yet has none, so its first turn can start.
func (c *Codex) activeTurnID(ctx context.Context, conn codexClient, threadID string) (string, error) {
	response, err := conn.Request(ctx, "thread/turns/list", map[string]any{
		"threadId":      threadID,
		"limit":         1,
		"sortDirection": "desc",
	}, 5*time.Second)
	if codexTurnsNotListedYet(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("thread/turns/list: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	for _, raw := range turns {
		turn, _ := raw.(map[string]any)
		status, _, _ := codexTurnState(turn["status"])
		if status == surface.StatusBusy {
			return str(turn, "id"), nil
		}
	}
	return "", nil
}

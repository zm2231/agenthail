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
	// A thread with no settled turn says how it stands only through its own
	// status; one Codex has not loaded is loaded again by a send.
	if observation.Status == surface.StatusUnknown {
		if notLoaded, err := codexThreadReadsNotLoaded(ctx, conn, sess.ID); err == nil && notLoaded {
			observation.Status = surface.StatusNotLoaded
		}
	}
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
	observation = dropUnconfirmedTurn(ctx, conn, thread, observation)
	if path := codexTranscriptPath(sess); path != "" {
		if info, err := os.Stat(path); err == nil {
			observation.Plan = c.plan(ctx, path, info)
			observation.BackgroundTasks = c.codexBackground(ctx, sess.ID, path, info)
		}
	}
	return observation, nil
}

const codexTurnInterrupted = "turn interrupted"

// A process that hasn't loaded a thread reads its running turn as interrupted,
// so that status from such a reader says nothing about the turn. Its completed
// status is reliable.
func codexThreadNotLoaded(ctx context.Context, conn codexClient, threadID string) bool {
	notLoaded, err := codexThreadReadsNotLoaded(ctx, conn, threadID)
	return err != nil || notLoaded
}

func codexThreadReadsNotLoaded(ctx context.Context, conn codexClient, threadID string) (bool, error) {
	response, err := conn.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return false, err
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	status, _ := thread["status"].(map[string]any)
	return str(status, "type") == "notLoaded", nil
}

func dropUnconfirmedTurn(ctx context.Context, conn codexClient, thread *codexThread, observation *surface.TurnObservation) *surface.TurnObservation {
	for index := len(thread.Turns) - 1; index >= 0; index-- {
		turn := thread.Turns[index]
		if turn.ID == "" || !turn.Done {
			continue
		}
		if turn.Error != codexTurnInterrupted || !codexThreadNotLoaded(ctx, conn, thread.ID) {
			return observation
		}
		thread.Turns = append(thread.Turns[:index:index], thread.Turns[index+1:]...)
		observation = codexObservation(thread)
		observation.TurnUnconfirmed = true
		return observation
	}
	return observation
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

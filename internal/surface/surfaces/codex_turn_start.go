package surfaces

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

var (
	codexTurnStartWait  = 10 * time.Second
	codexTurnStartLimit = 5 * time.Minute
)

// codexTurnStarts records the turns this process started that Codex has not
// yet shown under way. Codex lists a turn as in progress as soon as turn/start
// returns, except a new thread's first turn, whose thread refuses to list
// turns until that turn ends.
type codexTurnStarts struct {
	mu      sync.Mutex
	started map[string]time.Time
}

func (s *codexTurnStarts) begin(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started == nil {
		s.started = map[string]time.Time{}
	}
	s.started[threadID] = time.Now()
}

func (s *codexTurnStarts) end(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.started, threadID)
}

func (s *codexTurnStarts) pending(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	at, ok := s.started[threadID]
	return ok && time.Since(at) < codexTurnStartLimit
}

// startTurn sends a request that starts a turn on threadID, then keeps client
// open, and so subscribed, until Codex reports the thread active or the turn
// completed, for at most codexTurnStartWait. Until then Codex counts the
// thread idle, and a resume whose overrides differ reloads an idle thread no
// connection subscribes to, aborting the turn.
func (c *Codex) startTurn(ctx context.Context, client codexClient, threadID, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	c.turnStarts.begin(threadID)
	response, err := client.Request(ctx, method, params, wait)
	var refused *codexRPCError
	if errors.As(err, &refused) {
		c.turnStarts.end(threadID)
	}
	if err != nil {
		return response, err
	}
	if awaitTurnUnderway(ctx, client, threadID) {
		c.turnStarts.end(threadID)
	}
	return response, nil
}

func awaitTurnUnderway(ctx context.Context, client codexClient, threadID string) bool {
	notifier, ok := client.(codexNotifier)
	if !ok {
		return false
	}
	timer := time.NewTimer(codexTurnStartWait)
	defer timer.Stop()
	for {
		for _, event := range notifier.DrainNotifications() {
			if str(event.Params, "threadId") != threadID {
				continue
			}
			if event.Method == "turn/completed" || event.Method == "thread/status/changed" && codexStatusType(event.Params["status"]) == "active" {
				return true
			}
		}
		select {
		case <-notifier.Notified():
		case <-timer.C:
			return false
		case <-ctx.Done():
			return false
		}
	}
}

// turnInFlight reports whether a turn on threadID is starting or running, so
// a reload now would abort it. Right after turn/start Codex still reports the
// thread idle but already lists the turn in progress.
func (c *Codex) turnInFlight(ctx context.Context, client codexClient, threadID string) (bool, error) {
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	switch codexStatusType(thread["status"]) {
	case "active":
		c.turnStarts.end(threadID)
		return true, nil
	case "notLoaded":
		return false, nil
	}
	response, err = client.Request(ctx, "thread/turns/list", map[string]any{"threadId": threadID, "limit": 1, "sortDirection": "desc", "itemsView": "notLoaded"}, 5*time.Second)
	if codexTurnsNotListedYet(err) {
		return c.turnStarts.pending(threadID), nil
	}
	if err != nil {
		return false, fmt.Errorf("thread/turns/list: %w", err)
	}
	result, _ = response["result"].(map[string]any)
	turns, _ := result["data"].([]any)
	if len(turns) > 0 {
		turn, _ := turns[0].(map[string]any)
		if _, done, _ := codexTurnState(turn["status"]); !done {
			c.turnStarts.end(threadID)
			return true, nil
		}
	}
	return c.turnStarts.pending(threadID), nil
}

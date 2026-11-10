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
	codexTurnStartScan  = 20
)

// codexTurnStarts records the turns this process started that Codex has not
// yet shown under way. Codex lists a turn as in progress as soon as turn/start
// returns, except a new thread's first turn, whose thread refuses to list
// turns until that turn ends.
type codexTurnStarts struct {
	mu      sync.Mutex
	started map[string]codexTurnStart
}

type codexTurnStart struct {
	at     time.Time
	turnID string
	server *codexServer
}

func (s *codexTurnStarts) begin(threadID string, server *codexServer) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started == nil {
		s.started = map[string]codexTurnStart{}
	}
	s.started[threadID] = codexTurnStart{at: time.Now(), server: server}
}

// live returns threadID's start if it is still within codexTurnStartLimit
// and the app-server that ran it has not restarted, and forgets it otherwise.
func (s *codexTurnStarts) live(threadID string) (codexTurnStart, bool) {
	start, ok := s.started[threadID]
	if !ok {
		return start, false
	}
	if time.Since(start.at) >= codexTurnStartLimit || start.server != nil && start.server.restarted() {
		delete(s.started, threadID)
		return start, false
	}
	return start, true
}

func (s *codexTurnStarts) identify(threadID, turnID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if start, ok := s.started[threadID]; ok {
		start.turnID = turnID
		s.started[threadID] = start
	}
}

func (s *codexTurnStarts) end(threadID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.started, threadID)
}

// settle ends a start once turns, the newest first and none running, show its
// turn ended: listed with completedAt or behind a newer turn, or gone from a
// full page. A compaction whose turn its connection did not see holds until
// codexTurnStartLimit.
func (s *codexTurnStarts) settle(threadID string, turns []map[string]any, full bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	start, ok := s.started[threadID]
	if !ok {
		return
	}
	if start.turnID == "" {
		return
	}
	for i, turn := range turns {
		if str(turn, "id") == start.turnID {
			if i > 0 || turn["completedAt"] != nil {
				delete(s.started, threadID)
			}
			return
		}
	}
	if full {
		delete(s.started, threadID)
	}
}

func (s *codexTurnStarts) running(threadID, turnID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	start, ok := s.live(threadID)
	return ok && turnID != "" && start.turnID == turnID
}

func (s *codexTurnStarts) pending(threadID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.live(threadID)
	return ok
}

// startTurn sends a request that starts a turn on threadID, then keeps client
// open, and so subscribed, until Codex reports the thread active or the turn
// completed, for at most codexTurnStartWait. Until then Codex counts the
// thread idle, and a resume whose overrides differ reloads an idle thread no
// connection subscribes to, aborting the turn. The start stays recorded until
// its turn is listed ended. thread/compact/start names no turn and answers
// before Codex creates one, so its turn is the one this connection receives
// after the answer and sees run a contextCompaction item.
func (c *Codex) startTurn(ctx context.Context, client codexClient, threadID, method string, params map[string]any, wait time.Duration) (map[string]any, error) {
	var server *codexServer
	if managed, ok := client.(*managedCodexClient); ok {
		server = managed.server
	}
	if notifier, ok := client.(codexNotifier); ok {
		notifier.DrainNotifications()
	}
	c.turnStarts.begin(threadID, server)
	var response map[string]any
	var err error
	after := int64(-1)
	if ordered, ok := client.(codexOrderedClient); ok {
		response, after, err = ordered.requestOrdered(ctx, method, params, wait)
	} else {
		response, err = client.Request(ctx, method, params, wait)
	}
	var refused *codexRPCError
	if errors.As(err, &refused) {
		c.turnStarts.end(threadID)
	}
	if err != nil {
		return response, err
	}
	result, _ := response["result"].(map[string]any)
	turn, _ := result["turn"].(map[string]any)
	id := str(turn, "id")
	if id != "" {
		c.turnStarts.identify(threadID, id)
	}
	if found := awaitTurnUnderway(ctx, client, threadID, id == "", after); found != "" {
		c.turnStarts.identify(threadID, found)
	}
	return response, nil
}

func codexRecentTurns(ctx context.Context, client codexClient, threadID string, limit int) ([]map[string]any, error) {
	response, err := client.Request(ctx, "thread/turns/list", map[string]any{"threadId": threadID, "limit": limit, "sortDirection": "desc", "itemsView": "notLoaded"}, 5*time.Second)
	if err != nil {
		return nil, err
	}
	result, _ := response["result"].(map[string]any)
	values, _ := result["data"].([]any)
	turns := make([]map[string]any, 0, len(values))
	for _, value := range values {
		if turn, ok := value.(map[string]any); ok {
			turns = append(turns, turn)
		}
	}
	return turns, nil
}

// codexOrderedClient answers a request with the receive count of the
// notifications it read before the answer.
type codexOrderedClient interface {
	requestOrdered(context.Context, string, map[string]any, time.Duration) (map[string]any, int64, error)
}

// awaitTurnUnderway waits for threadID to turn active or its turn to
// complete. With findTurn it waits instead for the turn a compaction started
// and returns its id. after is the connection's receive count when the compact
// answer arrived; only a notification counted beyond it can name the turn, and
// a client that cannot count against its answers finds none.
func awaitTurnUnderway(ctx context.Context, client codexClient, threadID string, findTurn bool, after int64) string {
	notifier, ok := client.(codexNotifier)
	if !ok {
		return ""
	}
	timer := time.NewTimer(codexTurnStartWait)
	defer timer.Stop()
	started := map[string]bool{}
	for {
		for _, event := range notifier.DrainNotifications() {
			if str(event.Params, "threadId") != threadID {
				continue
			}
			turn, _ := event.Params["turn"].(map[string]any)
			item, _ := event.Params["item"].(map[string]any)
			switch {
			case !findTurn:
				if event.Method == "turn/completed" || event.Method == "thread/status/changed" && codexStatusType(event.Params["status"]) == "active" {
					return ""
				}
			case event.Method == "turn/started" && after >= 0 && event.received > after:
				started[str(turn, "id")] = true
			case event.Method == "item/started" && str(item, "type") == "contextCompaction" && started[str(event.Params, "turnId")]:
				return str(event.Params, "turnId")
			}
		}
		select {
		case <-notifier.Notified():
		case <-timer.C:
			return ""
		case <-ctx.Done():
			return ""
		}
	}
}

// turnInFlight reports whether a turn on threadID is starting or running, so
// a reload now would abort it. Right after turn/start Codex can still report
// the thread idle and list the turn interrupted with no completedAt, and a new
// thread's first turn is not listed until it ends, so a start this process
// recorded counts until its turn is listed with completedAt or behind a newer
// turn.
func (c *Codex) turnInFlight(ctx context.Context, client codexClient, threadID string) (bool, error) {
	response, err := client.Request(ctx, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false}, 5*time.Second)
	if err != nil {
		return false, fmt.Errorf("thread/read: %w", err)
	}
	result, _ := response["result"].(map[string]any)
	thread, _ := result["thread"].(map[string]any)
	switch codexStatusType(thread["status"]) {
	case "active":
		return true, nil
	case "notLoaded":
		return false, nil
	}
	turns, err := codexRecentTurns(ctx, client, threadID, codexTurnStartScan)
	if codexTurnsNotListedYet(err) {
		return c.turnStarts.pending(threadID), nil
	}
	if err != nil {
		return false, fmt.Errorf("thread/turns/list: %w", err)
	}
	for _, turn := range turns {
		if _, finished, _ := codexTurnState(turn["status"]); !finished {
			return true, nil
		}
	}
	c.turnStarts.settle(threadID, turns, len(turns) >= codexTurnStartScan)
	return c.turnStarts.pending(threadID), nil
}

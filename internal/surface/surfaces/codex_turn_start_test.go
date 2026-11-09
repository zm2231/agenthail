package surfaces

import (
	"context"
	"fmt"
	"testing"
	"time"
)

type listedTurnClient struct{ turns []any }

func (c listedTurnClient) Request(_ context.Context, method string, _ map[string]any, _ time.Duration) (map[string]any, error) {
	switch method {
	case "thread/read":
		return map[string]any{"result": map[string]any{"thread": map[string]any{"status": map[string]any{"type": "idle"}}}}, nil
	case "thread/turns/list":
		return map[string]any{"result": map[string]any{"data": c.turns}}, nil
	}
	return map[string]any{"result": map[string]any{}}, nil
}

func (listedTurnClient) Close() error { return nil }

func listedTurn(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "completedAt": 1.79e9}
}

func TestAStartCodexNeverShowedUnderWayHoldsReloadsOnlyUntilItsTurnIsListedDone(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t", nil)
	c.turnStarts.identify("t", "turn-2")
	older := listedTurnClient{turns: []any{listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), older, "t"); err != nil || !busy {
		t.Fatalf("a start whose turn is not listed yet let a reload through: busy=%v err=%v", busy, err)
	}
	done := listedTurnClient{turns: []any{listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), done, "t"); err != nil || busy {
		t.Fatalf("a start whose turn is listed done still holds reloads: busy=%v err=%v", busy, err)
	}
	if c.turnStarts.pending("t") {
		t.Fatal("the start stayed recorded after its turn was listed done")
	}
}

func TestAStartWhoseTurnEndedBeforeANewerOneNoLongerHoldsReloads(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t", nil)
	c.turnStarts.identify("t", "turn-1")
	later := listedTurnClient{turns: []any{listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), later, "t"); err != nil || busy {
		t.Fatalf("a start whose turn ended behind a newer one still holds reloads: busy=%v err=%v", busy, err)
	}
}

func TestAStartListedInterruptedBeforeTheThreadTurnsActiveStillHoldsReloads(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t", nil)
	c.turnStarts.identify("t", "turn-2")
	starting := listedTurnClient{turns: []any{map[string]any{"id": "turn-2", "status": "interrupted", "completedAt": nil}, listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), starting, "t"); err != nil || !busy {
		t.Fatalf("a start Codex lists interrupted before it turns active let a reload through: busy=%v err=%v", busy, err)
	}
}

func TestAStartWhoseTurnFellPastTheScannedTurnsNoLongerHoldsReloads(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t", nil)
	c.turnStarts.identify("t", "turn-0")
	newer := listedTurnClient{}
	for i := codexTurnStartScan; i > 0; i-- {
		newer.turns = append(newer.turns, listedTurn(fmt.Sprintf("turn-%d", i), "completed"))
	}
	if busy, err := c.turnInFlight(context.Background(), newer, "t"); err != nil || busy {
		t.Fatalf("a start behind a full page of newer ended turns still holds reloads: busy=%v err=%v", busy, err)
	}
}

func TestACompactionHoldsReloadsUntilTheStartLimitWhateverTurnsAreListed(t *testing.T) {
	c := &Codex{}
	before := []any{listedTurn("turn-1", "completed")}
	if _, err := c.startTurn(context.Background(), listedTurnClient{turns: before}, "t", "thread/compact/start", map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	for _, turns := range [][]any{nil, before, append([]any{listedTurn("turn-2", "completed")}, before...)} {
		if busy, err := c.turnInFlight(context.Background(), listedTurnClient{turns: turns}, "t"); err != nil || !busy {
			t.Fatalf("a compaction let a reload through over %v: busy=%v err=%v", turns, busy, err)
		}
	}
	limit := codexTurnStartLimit
	codexTurnStartLimit = 0
	t.Cleanup(func() { codexTurnStartLimit = limit })
	if busy, err := c.turnInFlight(context.Background(), listedTurnClient{turns: before}, "t"); err != nil || busy {
		t.Fatalf("a compaction past the start limit still holds reloads: busy=%v err=%v", busy, err)
	}
}

func TestATurnThisProcessStartedIsRunningWhileCodexListsItUnsettled(t *testing.T) {
	wait := codexUnsettledTurnWait
	codexUnsettledTurnWait = 0
	t.Cleanup(func() { codexUnsettledTurnWait = wait })
	c := &Codex{}
	starting := listedTurnClient{turns: []any{map[string]any{"id": "turn-2", "status": "interrupted", "completedAt": nil}, listedTurn("turn-1", "completed")}}
	if id, err := c.activeTurnID(context.Background(), starting, "t"); err != nil || id != "" {
		t.Fatalf("a turn another client started was taken as running without being listed running: %q %v", id, err)
	}
	c.turnStarts.begin("t", nil)
	c.turnStarts.identify("t", "turn-2")
	if id, err := c.activeTurnID(context.Background(), starting, "t"); err != nil || id != "turn-2" {
		t.Fatalf("a turn this process started read as not running: %q %v", id, err)
	}
}

type notifiedTurnClient struct {
	listedTurnClient
	unread   []codexEvent
	reply    []codexEvent
	events   []codexEvent
	received int64
	notified chan struct{}
}

func (c *notifiedTurnClient) DrainNotifications() []codexEvent {
	events := c.events
	c.events = nil
	return events
}

func (c *notifiedTurnClient) Notified() <-chan struct{} { return c.notified }

func (c *notifiedTurnClient) receive(events []codexEvent) {
	for _, event := range events {
		c.received++
		event.received = c.received
		c.events = append(c.events, event)
	}
}

func (c *notifiedTurnClient) requestOrdered(ctx context.Context, method string, params map[string]any, wait time.Duration) (map[string]any, int64, error) {
	if method != "thread/compact/start" {
		response, err := c.listedTurnClient.Request(ctx, method, params, wait)
		return response, c.received, err
	}
	c.receive(c.unread)
	after := c.received
	c.receive(c.reply)
	return map[string]any{"result": map[string]any{}}, after, nil
}

func turnStartedEvent(id string) codexEvent {
	return codexEvent{Method: "turn/started", Params: map[string]any{"threadId": "t", "turn": map[string]any{"id": id}}}
}

func itemStartedEvent(turnID, itemType string) codexEvent {
	return codexEvent{Method: "item/started", Params: map[string]any{"threadId": "t", "turnId": turnID, "item": map[string]any{"type": itemType}}}
}

func TestACompactionIsMatchedToItsOwnTurnNotAnotherClientsThatEndsFirst(t *testing.T) {
	c := &Codex{}
	other := listedTurnClient{turns: []any{listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	client := &notifiedTurnClient{listedTurnClient: other, notified: make(chan struct{}), reply: []codexEvent{
		turnStartedEvent("turn-2"),
		itemStartedEvent("turn-2", "agentMessage"),
		{Method: "turn/completed", Params: map[string]any{"threadId": "t", "turn": map[string]any{"id": "turn-2"}}},
		turnStartedEvent("turn-3"),
		itemStartedEvent("turn-3", "contextCompaction"),
	}}
	if _, err := c.startTurn(context.Background(), client, "t", "thread/compact/start", map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if busy, err := c.turnInFlight(context.Background(), other, "t"); err != nil || !busy {
		t.Fatalf("another client's ended turn released the compaction's hold: busy=%v err=%v", busy, err)
	}
	done := listedTurnClient{turns: []any{listedTurn("turn-3", "completed"), listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), done, "t"); err != nil || busy {
		t.Fatalf("the compaction's own ended turn did not release its hold: busy=%v err=%v", busy, err)
	}
}

func TestACompactionIsNotMatchedToAnEarlierTurnReceivedDuringItsRequest(t *testing.T) {
	wait := codexTurnStartWait
	codexTurnStartWait = 50 * time.Millisecond
	t.Cleanup(func() { codexTurnStartWait = wait })
	c := &Codex{}
	earlier := listedTurnClient{turns: []any{listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	client := &notifiedTurnClient{listedTurnClient: earlier, notified: make(chan struct{}), unread: []codexEvent{
		turnStartedEvent("turn-2"),
		itemStartedEvent("turn-2", "contextCompaction"),
	}}
	if _, err := c.startTurn(context.Background(), client, "t", "thread/compact/start", map[string]any{"threadId": "t"}, time.Second); err != nil {
		t.Fatal(err)
	}
	if busy, err := c.turnInFlight(context.Background(), earlier, "t"); err != nil || !busy {
		t.Fatalf("a turn received before the compaction was answered released its hold: busy=%v err=%v", busy, err)
	}
}

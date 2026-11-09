package surfaces

import (
	"context"
	"testing"
	"time"
)

type listedTurnClient struct{ turns []any }

func (c listedTurnClient) Request(_ context.Context, method string, _ map[string]any, _ time.Duration) (map[string]any, error) {
	if method == "thread/read" {
		return map[string]any{"result": map[string]any{"thread": map[string]any{"status": map[string]any{"type": "idle"}}}}, nil
	}
	return map[string]any{"result": map[string]any{"data": c.turns}}, nil
}

func (listedTurnClient) Close() error { return nil }

func listedTurn(id, status string) map[string]any {
	return map[string]any{"id": id, "status": status, "completedAt": 1.79e9}
}

func TestAStartCodexNeverShowedUnderWayHoldsReloadsOnlyUntilItsTurnIsListedDone(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t")
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
	c.turnStarts.begin("t")
	c.turnStarts.identify("t", "turn-1")
	later := listedTurnClient{turns: []any{listedTurn("turn-2", "completed"), listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), later, "t"); err != nil || busy {
		t.Fatalf("a start whose turn ended behind a newer one still holds reloads: busy=%v err=%v", busy, err)
	}
}

func TestAStartListedInterruptedBeforeTheThreadTurnsActiveStillHoldsReloads(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t")
	c.turnStarts.identify("t", "turn-2")
	starting := listedTurnClient{turns: []any{map[string]any{"id": "turn-2", "status": "interrupted", "completedAt": nil}, listedTurn("turn-1", "completed")}}
	if busy, err := c.turnInFlight(context.Background(), starting, "t"); err != nil || !busy {
		t.Fatalf("a start Codex lists interrupted before it turns active let a reload through: busy=%v err=%v", busy, err)
	}
}

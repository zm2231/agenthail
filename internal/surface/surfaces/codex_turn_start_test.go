package surfaces

import (
	"context"
	"testing"
	"time"
)

type listedTurnClient struct{ newest map[string]any }

func (c listedTurnClient) Request(_ context.Context, method string, _ map[string]any, _ time.Duration) (map[string]any, error) {
	if method == "thread/read" {
		return map[string]any{"result": map[string]any{"thread": map[string]any{"status": map[string]any{"type": "idle"}}}}, nil
	}
	return map[string]any{"result": map[string]any{"data": []any{c.newest}}}, nil
}

func (listedTurnClient) Close() error { return nil }

func TestAStartCodexNeverShowedUnderWayHoldsReloadsOnlyUntilItsTurnIsListedDone(t *testing.T) {
	c := &Codex{}
	c.turnStarts.begin("t")
	c.turnStarts.identify("t", "turn-2")
	older := listedTurnClient{newest: map[string]any{"id": "turn-1", "status": "completed"}}
	if busy, err := c.turnInFlight(context.Background(), older, "t"); err != nil || !busy {
		t.Fatalf("a start whose turn is not listed yet let a reload through: busy=%v err=%v", busy, err)
	}
	done := listedTurnClient{newest: map[string]any{"id": "turn-2", "status": "completed"}}
	if busy, err := c.turnInFlight(context.Background(), done, "t"); err != nil || busy {
		t.Fatalf("a start whose turn is listed done still holds reloads: busy=%v err=%v", busy, err)
	}
	if c.turnStarts.pending("t") {
		t.Fatal("the start stayed recorded after its turn was listed done")
	}
}

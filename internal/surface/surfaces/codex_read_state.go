package surfaces

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexReadStateKey = "readState"

func codexReadState(value any) *surface.ReadState {
	object, _ := value.(map[string]any)
	revision := str(object, "revision")
	if revision == "" {
		return nil
	}
	state := &surface.ReadState{Revision: revision}
	position, _ := object["firstUnread"].(map[string]any)
	switch str(position, "type") {
	case "turn":
		state.FirstUnread = str(position, "turnId")
	case surface.ReadFromStart:
		state.FirstUnread = surface.ReadFromStart
	}
	return state
}

// thread/list keeps receipts beside the threads, not on them.
func attachCodexReadStates(result map[string]any, threads []any) {
	receipts, _ := result["readStates"].(map[string]any)
	for _, value := range threads {
		if thread, _ := value.(map[string]any); thread != nil {
			if receipt, ok := receipts[str(thread, "id")]; ok {
				thread[codexReadStateKey] = receipt
			}
		}
	}
}

func (c *Codex) SetReadState(ctx context.Context, sess *surface.Session, unread bool, revision string) (surface.ReadState, error) {
	runtimes := c.openRuntimes(ctx)
	defer runtimes.Close()
	clients := runtimes.readers()
	if len(clients) == 0 {
		return surface.ReadState{}, runtimes.unavailable()
	}
	operation := "read"
	if unread {
		operation = "unread"
	}
	params := map[string]any{"threadId": sess.ID, "expectedRevision": revision, "operation": map[string]any{"type": operation}}
	var err error
	for _, client := range clients {
		var response map[string]any
		response, err = client.Request(ctx, "thread/readState/update", params, 5*time.Second)
		// The bridge's own refusals carry no JSON-RPC code; the next runtime may still answer.
		var rpcErr *codexRPCError
		if errors.As(err, &rpcErr) && rpcErr.Code != 0 {
			return surface.ReadState{}, codexReadStateRefusal(rpcErr)
		}
		if err != nil {
			continue
		}
		result, _ := response["result"].(map[string]any)
		if state := codexReadState(result[codexReadStateKey]); state != nil {
			return *state, nil
		}
		return surface.ReadState{}, fmt.Errorf("thread/readState/update response missing readState")
	}
	return surface.ReadState{}, err
}

func codexReadStateRefusal(err *codexRPCError) error {
	data, _ := err.Data.(map[string]any)
	if str(data, "reason") == "readStateConflict" {
		if current := codexReadState(data[codexReadStateKey]); current != nil {
			return &surface.ReadStateConflict{Current: *current}
		}
	}
	return err
}

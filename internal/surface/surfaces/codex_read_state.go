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

func (c *Codex) OpenRead(ctx context.Context, sess *surface.Session, revision string, mark bool) (*surface.ReadState, error) {
	client, err := c.openRunningWriter(ctx, sess)
	if err != nil && !mark {
		return nil, nil
	}
	if err != nil {
		if client, err = c.openReader(ctx, sess); err != nil {
			return nil, err
		}
	}
	defer client.Close()
	if !mark {
		return nil, nil
	}
	state, err := c.updateReadState(ctx, client, sess.ID, "read", revision)
	if err != nil {
		return nil, err
	}
	return &state, nil
}

func (c *Codex) MarkUnread(ctx context.Context, sess *surface.Session, revision string) (surface.ReadState, error) {
	client, err := c.openReader(ctx, sess)
	if err != nil {
		return surface.ReadState{}, err
	}
	defer client.Close()
	return c.updateReadState(ctx, client, sess.ID, "unread", revision)
}

// Opening a conversation never starts Codex's daemon, and never takes Desktop's writer: a send does the starting.
func (c *Codex) openRunningWriter(ctx context.Context, sess *surface.Session) (codexClient, error) {
	client, err := c.openRouteStarting(ctx, sess, false)
	if err != nil {
		return nil, err
	}
	if err := c.requireDirectInput(ctx, client, sess); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

func (c *Codex) updateReadState(ctx context.Context, client codexClient, threadID, operation, revision string) (surface.ReadState, error) {
	response, err := client.Request(ctx, "thread/readState/update", map[string]any{"threadId": threadID, "expectedRevision": revision, "operation": map[string]any{"type": operation}}, 5*time.Second)
	if rpcErr := (*codexRPCError)(nil); errors.As(err, &rpcErr) {
		return surface.ReadState{}, codexReadStateRefusal(rpcErr)
	}
	if err != nil {
		return surface.ReadState{}, err
	}
	result, _ := response["result"].(map[string]any)
	if state := codexReadState(result[codexReadStateKey]); state != nil {
		return *state, nil
	}
	return surface.ReadState{}, fmt.Errorf("thread/readState/update response missing readState")
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

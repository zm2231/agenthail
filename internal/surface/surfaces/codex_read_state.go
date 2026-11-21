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
	client, err := c.readStateClient(ctx, sess, unread)
	if err != nil {
		return surface.ReadState{}, err
	}
	defer client.Close()
	operation := "read"
	if unread {
		operation = "unread"
	}
	response, err := client.Request(ctx, "thread/readState/update", map[string]any{"threadId": sess.ID, "expectedRevision": revision, "operation": map[string]any{"type": operation}}, 5*time.Second)
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

// Reading a conversation loads it into its writer as a send would; one no writer can take, such as Desktop's while Desktop is down, is only marked.
func (c *Codex) readStateClient(ctx context.Context, sess *surface.Session, unread bool) (codexClient, error) {
	if !unread {
		if client, err := c.openWriter(ctx, sess); err == nil {
			return client, nil
		}
	}
	return c.openReader(ctx, sess)
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

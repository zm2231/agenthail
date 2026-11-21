package surface

import (
	"context"
	"fmt"
)

type queueItemContextKey struct{}

func WithQueueItem(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, queueItemContextKey{}, id)
}

func ResumeCause(ctx context.Context) string {
	if id, ok := ctx.Value(queueItemContextKey{}).(int64); ok {
		return fmt.Sprintf("queue item %d", id)
	}
	return "a request outside the queue"
}

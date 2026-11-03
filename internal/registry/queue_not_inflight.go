package registry

import (
	"errors"
	"fmt"
)

// ErrQueueItemNotInflight means the item was settled by someone else after it was claimed.
var ErrQueueItemNotInflight = errors.New("queue item is not inflight")

func queueItemNotInflight(id int64) error {
	return fmt.Errorf("queue item %d: %w", id, ErrQueueItemNotInflight)
}

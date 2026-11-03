package daemon

import (
	"errors"

	"github.com/zm2231/agenthail/internal/registry"
)

func (d *Daemon) settledElsewhere(queueID int64, err error) bool {
	if !errors.Is(err, registry.ErrQueueItemNotInflight) {
		return false
	}
	d.log.Printf("queue item %d was settled while this attempt ran; its outcome is not recorded", queueID)
	return true
}

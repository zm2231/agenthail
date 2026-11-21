package claudesurface

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) StillHolds(conversation string, holder surface.Holder) bool {
	_, held := c.heldRecord(conversation, holder)
	return held
}

// Linux dates a process start up to a second early, so one that started within a second of at is not known to predate it.
const processStartSlack = time.Second

// A pid is reused by a process that starts later, whichever start its record names.
func (c *Claude) StartedBefore(holder surface.Holder, at time.Time) bool {
	if !holder.StartedAt.IsZero() && !holder.StartedAt.Before(at) {
		return false
	}
	started, err := processStartedAt(holder.PID)
	return err == nil && started.Before(at.Add(-processStartSlack))
}

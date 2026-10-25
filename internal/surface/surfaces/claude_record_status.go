package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) applyRecordStatus(ctx context.Context, sess *surface.Session, record map[string]any) {
	if ts, ok := record["updatedAt"].(float64); ok && ts > 0 {
		sess.LastActive = time.UnixMilli(int64(ts))
	}
	sess.Status = claudePeerStatus(record)
	sess.WaitingOn = c.prompts.answeredWait(sess.ID, c.recordWaiting(sess))
	sess.BackgroundTasks = nil
	sess.UsageLimit = nil
	sess.Activity = nil
	sess.Plan = nil
	if sess.HasLocal {
		if observation, observeErr := c.Observe(ctx, sess); observeErr == nil && observation.Status != surface.StatusUnknown {
			sess.Status = observation.Status
			sess.WaitingOn = observation.WaitingOn
			sess.BackgroundTasks = observation.BackgroundTasks
			sess.UsageLimit = observation.UsageLimit
			sess.Activity = observation.Activity
			sess.Plan = observation.Plan
		}
	}
	if sess.Activity != nil && sess.Activity.LastProgressAt.After(sess.LastActive) {
		sess.LastActive = sess.Activity.LastProgressAt
	}
}

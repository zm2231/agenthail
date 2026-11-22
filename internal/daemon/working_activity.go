package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (d *Daemon) turnWaited(session surface.Session) time.Duration {
	start := session.Activity.TurnStart()
	if start.IsZero() {
		return 0
	}
	waited, err := d.Registry.TurnWaited(session.ID, start)
	if err != nil {
		d.log.Printf("read the waits of %s: %s", session.ID, err)
		return 0
	}
	return waited
}

func workingActivity(activity *surface.Activity, waited time.Duration) *surface.Activity {
	start := activity.TurnStart()
	if start.IsZero() {
		return activity
	}
	working := *activity
	working.TurnStartedAt = start
	working.BusySince = start.Add(waited)
	return &working
}

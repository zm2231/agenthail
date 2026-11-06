package daemon

import (
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	InboxNeedsYou = "needsYou"
	InboxRunning  = "running"
	InboxSnoozed  = "snoozed"
	InboxArchived = "archived"
)

type dashboardSnooze struct {
	SnoozedAt time.Time `json:"snoozedAt"`
	Until     time.Time `json:"until,omitzero"`
	Wake      string    `json:"wake"`
}

func snoozeRow(snooze registry.Snooze) *dashboardSnooze {
	if !snooze.Set() {
		return nil
	}
	return &dashboardSnooze{SnoozedAt: snooze.At, Until: snooze.Until, Wake: snooze.Wake}
}

func rowRunning(row dashboardSession) bool {
	if row.Display == nil {
		return false
	}
	switch row.Display.Kind {
	case "working", "finishing", "queued":
		return true
	}
	return false
}

func snoozeHolds(row dashboardSession, now time.Time) bool {
	snooze := row.Snooze
	if snooze == nil {
		return false
	}
	switch snooze.Wake {
	case registry.SnoozeWakeTime:
		return now.Before(snooze.Until)
	case registry.SnoozeWakeFinished:
		if !rowRunning(row) {
			return false
		}
		return row.Activity == nil || row.Activity.BusySince.IsZero() || !row.Activity.BusySince.Truncate(time.Millisecond).After(snooze.SnoozedAt)
	}
	return false
}

// Attention outranks a snooze so snoozing never hides a request.
func sessionInbox(row dashboardSession, now time.Time) string {
	switch {
	case row.Archived:
		return InboxArchived
	case row.Attention != nil:
		return InboxNeedsYou
	case snoozeHolds(row, now):
		return InboxSnoozed
	case rowRunning(row):
		return InboxRunning
	}
	return ""
}

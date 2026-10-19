package surface

import (
	"testing"
	"time"
)

func TestDefaultPresenceCountsWorkQueueAndTheLastDay(t *testing.T) {
	now := time.Date(2026, 7, 12, 20, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name       string
		session    Session
		queueCount int
		want       string
	}{
		{"working", Session{Status: StatusBusy}, 0, "working"},
		{"queued", Session{Status: StatusIdle}, 1, "queued"},
		{"recent", Session{Status: StatusIdle, LastActive: now.Add(-23 * time.Hour)}, 0, "recent"},
		{"stale", Session{Status: StatusIdle, LastActive: now.Add(-25 * time.Hour)}, 0, ""},
	} {
		current, reason := PresenceFor(sendOnlyFake{}, test.session, PresenceInput{QueueCount: test.queueCount, Now: now})
		if reason != test.want || current != (test.want != "") {
			t.Fatalf("%s: current=%v reason=%q", test.name, current, reason)
		}
	}
}

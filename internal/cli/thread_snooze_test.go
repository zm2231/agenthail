package cli

import (
	"testing"
	"time"
)

func TestSnoozeUntilTakesADurationOrATime(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	if at, err := parseSnoozeUntil("90m", now); err != nil || !at.Equal(now.Add(90*time.Minute)) {
		t.Fatalf("90m: %v %v", at, err)
	}
	if at, err := parseSnoozeUntil("2026-11-07T09:00:00-05:00", now); err != nil || !at.Equal(time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC)) {
		t.Fatalf("RFC 3339: %v %v", at, err)
	}
	for _, bad := range []string{"-1h", "0s", "tomorrow"} {
		if _, err := parseSnoozeUntil(bad, now); err == nil {
			t.Errorf("%q was accepted", bad)
		}
	}
}

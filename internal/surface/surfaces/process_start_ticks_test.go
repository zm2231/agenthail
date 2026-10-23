package surfaces

import (
	"testing"
	"time"
)

func TestALinuxStartTimeIsExactForAnyUptime(t *testing.T) {
	const boot = 1_700_000_000
	for _, test := range []struct {
		ticks int64
		want  time.Time
	}{
		{0, time.Unix(boot, 0)},
		{12_345, time.Unix(boot+123, 450_000_000)},
		// Twelve years of uptime, past what a Duration in nanoseconds holds.
		{37_869_120_001, time.Unix(boot+378_691_200, 10_000_000)},
	} {
		if got := linuxStartTime(boot, test.ticks); !got.Equal(test.want) {
			t.Fatalf("ticks %d: start %s, want %s", test.ticks, got, test.want)
		}
	}
}

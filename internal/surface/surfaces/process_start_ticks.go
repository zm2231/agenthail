package surfaces

import "time"

// linuxClockTicks is USER_HZ, the unit of /proc/<pid>/stat's starttime, which
// Linux fixes at 100 on every architecture it reports it for.
const linuxClockTicks = 100

// linuxStartTime is the wall time of a process that started ticks after a
// boot at boot seconds. Whole seconds and the remainder convert apart, since
// ticks past about three years of uptime overflow a Duration in nanoseconds.
func linuxStartTime(boot, ticks int64) time.Time {
	return time.Unix(boot+ticks/linuxClockTicks, (ticks%linuxClockTicks)*int64(time.Second)/linuxClockTicks)
}

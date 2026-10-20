package surfaces

import (
	"fmt"
	"time"

	"golang.org/x/sys/unix"
)

func processStartTime(pid int) (time.Time, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return time.Time{}, err
	}
	return time.Unix(info.Proc.P_starttime.Sec, 0), nil
}

// processIdentity names a running process by its start time to the
// microsecond, which tells it from a later process given the same pid.
func processIdentity(pid int) (string, error) {
	info, err := unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d.%06d", info.Proc.P_starttime.Sec, info.Proc.P_starttime.Usec), nil
}

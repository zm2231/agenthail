package surfaces

import (
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

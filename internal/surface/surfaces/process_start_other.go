//go:build !darwin

package surfaces

import (
	"context"
	"errors"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/procstart"
)

func processStartTime(pid int) (time.Time, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	output, err := procstart.Lstart(ctx, pid)
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation(claudeProcessStartLayout, output, time.UTC)
}

// processIdentity names a running process by its start time, which tells it
// from a later process given the same pid: in clock ticks on Linux, and to
// the second from ps elsewhere.
func processIdentity(pid int) (string, error) {
	if runtime.GOOS == "linux" {
		return procstart.Ticks(pid)
	}
	started, err := processStartTime(pid)
	if err != nil {
		return "", err
	}
	return started.UTC().Format(time.RFC3339), nil
}

// processStartedAt is when a running process started: from its start ticks
// and the boot time on Linux, which reads two files instead of running ps, and
// to the second from ps elsewhere. Linux's boot time is whole seconds, so the
// result may be up to a second early.
func processStartedAt(pid int) (time.Time, error) {
	if runtime.GOOS != "linux" {
		return processStartTime(pid)
	}
	raw, err := procstart.Ticks(pid)
	if err != nil {
		return time.Time{}, err
	}
	ticks, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, err
	}
	stat, err := os.ReadFile("/proc/stat")
	if err != nil {
		return time.Time{}, err
	}
	for _, line := range strings.Split(string(stat), "\n") {
		if value, found := strings.CutPrefix(line, "btime "); found {
			boot, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
			if err != nil {
				return time.Time{}, err
			}
			return linuxStartTime(boot, ticks), nil
		}
	}
	return time.Time{}, errors.New("/proc/stat has no boot time")
}

// Package procstart reads when a process started, which tells it from a
// later process given the same pid.
package procstart

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

// Lstart is the start time ps reports for pid, in UTC.
func Lstart(ctx context.Context, pid int) (string, error) {
	command := exec.CommandContext(ctx, "ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	out, err := command.Output()
	return strings.TrimSpace(string(out)), err
}

// Ticks is the starttime field of /proc/<pid>/stat on Linux, in clock ticks
// since boot.
func Ticks(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return "", err
	}
	return statStartTime(string(data))
}

func statStartTime(stat string) (string, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", fmt.Errorf("malformed /proc stat")
	}
	return fields[19], nil
}

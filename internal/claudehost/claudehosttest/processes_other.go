//go:build !darwin

package claudehosttest

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func processes() (map[int]string, error) {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, fmt.Errorf("list processes: %w", err)
	}
	out := map[int]string{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		if command := commandLine(pid); command != "" {
			out[pid] = command
		}
	}
	return out, nil
}

func commandLine(pid int) string {
	raw, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "cmdline"))
	if err != nil {
		return ""
	}
	return strings.Join(strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00"), " ")
}

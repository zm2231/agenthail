package peerproc

import (
	"os"
	"path/filepath"
	"strconv"
)

func OpenFiles(pid int) ([]string, error) {
	dir := filepath.Join("/proc", strconv.Itoa(pid), "fd")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(dir, entry.Name()))
		if err == nil && filepath.IsAbs(target) {
			paths = append(paths, target)
		}
	}
	return paths, nil
}

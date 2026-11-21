package claudesurface

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type unreadableRecord struct {
	pid  int
	path string
}

// A live process whose record cannot be read may hold any conversation.
func (c *Claude) unreadableRecords() []unreadableRecord {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil
	}
	var out []unreadableRecord
	for _, root := range roots {
		dir := filepath.Join(root, "sessions")
		entries, _ := os.ReadDir(dir)
		for _, entry := range entries {
			name, ok := strings.CutSuffix(entry.Name(), ".json")
			pid, err := strconv.Atoi(name)
			if !ok || err != nil || pid <= 0 || !claudeProcessAlive(pid) {
				continue
			}
			path := filepath.Join(dir, entry.Name())
			raw, err := os.ReadFile(path)
			var record map[string]any
			if err != nil || surface.DecodeClaudeSessionRecord(path, raw, &record) == nil {
				continue
			}
			info, err := os.Stat(path)
			started, startErr := processStartedAt(pid)
			if err != nil || startErr != nil || info.ModTime().Before(started.Add(-time.Second)) {
				continue
			}
			out = append(out, unreadableRecord{pid: pid, path: path})
		}
	}
	return out
}

func refuseUnknownHolders(conversation string, unreadable []unreadableRecord) error {
	first := unreadable[0]
	return surface.DeliveryUnavailable(fmt.Errorf("%w: Claude process %d runs but its session record %s cannot be read, so it may hold conversation %s; Agenthail does not open a second copy in the Claude host until the record reads", surface.ErrDeliveryHeld, first.pid, first.path, conversation))
}

func (c *Claude) UnreadableRecord(pid int) (string, bool) {
	for _, record := range c.unreadableRecords() {
		if record.pid == pid {
			return record.path, true
		}
	}
	return "", false
}

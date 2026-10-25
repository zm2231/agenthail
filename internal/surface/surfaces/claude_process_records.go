package surfaces

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
)

// claudeSessionRecord is a Claude process's session record and the config
// root it was written under.
type claudeSessionRecord struct {
	root   string
	fields map[string]any
}

// records returns the config roots and the session records of Claude
// processes that are alive in any of them, excluding the records Agenthail
// writes for its own peer workers and relays, and those of the processes the
// host starts only to list commands.
func (c *Claude) records() ([]string, []claudeSessionRecord, error) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil {
		return nil, nil, err
	}
	var out []claudeSessionRecord
	for _, root := range roots {
		sessionsDir := filepath.Join(root, "sessions")
		entries, err := os.ReadDir(sessionsDir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, nil, fmt.Errorf("read Claude session directory: %w", err)
		}
		for _, e := range entries {
			if !strings.HasSuffix(e.Name(), ".json") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(sessionsDir, e.Name()))
			if err != nil {
				continue
			}
			var m map[string]any
			if json.Unmarshal(raw, &m) != nil || str(m, "agenthail") != "" || strings.HasPrefix(str(m, "sessionId"), claudehost.ListingSessionPrefix) {
				continue
			}
			if pid, ok := m["pid"].(float64); ok && pid > 0 && !claudeRecordLive(m) {
				continue
			}
			out = append(out, claudeSessionRecord{root: root, fields: m})
		}
	}
	return roots, out, nil
}

// pidRecord reads the session record Claude process pid wrote, from the
// config root that holds it. When records for the pid sit in more than one
// root, the one its live process wrote wins.
func (c *Claude) pidRecord(pid int) (string, map[string]any, bool) {
	roots, err := surface.ClaudeConfigRoots(c.home)
	if err != nil || pid <= 0 {
		return "", nil, false
	}
	paths := surface.ClaudeSessionRecordPaths(roots, strconv.Itoa(pid))
	var firstPath string
	var first map[string]any
	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var record map[string]any
		if json.Unmarshal(raw, &record) != nil {
			continue
		}
		if len(paths) == 1 || claudeRecordLive(record) {
			return path, record, true
		}
		if first == nil {
			firstPath, first = path, record
		}
	}
	return firstPath, first, first != nil
}

func (c *Claude) peerRecordPath(pid int) string {
	if path, _, ok := c.pidRecord(pid); ok {
		return path
	}
	return filepath.Join(surface.ClaudeConfigDir(c.home), "sessions", strconv.Itoa(pid)+".json")
}

func claudeProcessAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || errors.Is(err, syscall.EPERM)
}

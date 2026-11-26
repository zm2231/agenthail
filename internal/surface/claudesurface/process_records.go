package claudesurface

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

// claudeSessionRecord is a Claude process's session record and the config
// root it was written under.
type claudeSessionRecord struct {
	root   string
	fields map[string]any
	readAt time.Time
}

// claudeScanAge bounds how long one scan reuses a read of Claude's files and
// background jobs, so a slow scan still sees a process that starts during it.
const claudeScanAge = time.Second

type claudeRecordsKey struct{ c *Claude }

type claudeRecordsRead struct {
	roots       []string
	records     []claudeSessionRecord
	readAt      time.Time
	transcripts func(string) []string
	err         error
}

// scanRecords is records and a transcript lookup over the same roots, read
// once for every session a scan in ctx observes.
func (c *Claude) scanRecords(ctx context.Context) claudeRecordsRead {
	return surface.ScanShared(ctx, claudeRecordsKey{c}, claudeScanAge, func() claudeRecordsRead {
		read := claudeRecordsRead{readAt: time.Now()}
		read.roots, read.records, read.err = c.records()
		if read.err == nil {
			read.transcripts = c.transcripts.pass(read.roots)
		}
		return read
	})
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
			readAt := time.Now()
			path := filepath.Join(sessionsDir, e.Name())
			raw, err := os.ReadFile(path)
			if err != nil {
				continue
			}
			var m map[string]any
			if surface.DecodeClaudeSessionRecord(path, raw, &m) != nil || transcript.String(m, "agenthail") != "" || strings.HasPrefix(transcript.String(m, "sessionId"), claudehost.ListingSessionPrefix) {
				continue
			}
			if pid, ok := m["pid"].(float64); ok && pid > 0 && !claudeRecordLive(m) {
				continue
			}
			out = append(out, claudeSessionRecord{root: root, fields: m, readAt: readAt})
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
		if surface.DecodeClaudeSessionRecord(path, raw, &record) != nil {
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

package surfaces

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// A saved scan is the state at its own offset, so an older one only costs
// rereading what follows it. The anchor hashes the bytes before the offset;
// a transcript rewritten under it is read again from the start.

func (c *Claude) taskStateDir() string {
	return filepath.Join(c.home, ".agenthail", "claude-tasks")
}

func (c *Claude) taskStatePath(transcript string) string {
	sum := sha256.Sum256([]byte(transcript))
	return filepath.Join(c.taskStateDir(), hex.EncodeToString(sum[:])+".json")
}

func newTaskScan(path string) *claudeTaskScan {
	return &claudeTaskScan{Path: path, Anchor: anchorOf(nil), Calls: map[string]surface.BackgroundTask{}}
}

func (c *Claude) loadTaskScan(path string, info os.FileInfo) *claudeTaskScan {
	fresh := newTaskScan(path)
	raw, err := os.ReadFile(c.taskStatePath(path))
	if err != nil {
		return fresh
	}
	var saved claudeTaskScan
	if json.Unmarshal(raw, &saved) != nil || saved.Path != path || saved.Offset < 0 || saved.Offset > info.Size() {
		return fresh
	}
	if !saved.matches(path) {
		return fresh
	}
	if saved.Calls == nil {
		saved.Calls = map[string]surface.BackgroundTask{}
	}
	return &saved
}

func (s *claudeTaskScan) matches(path string) bool {
	anchor, err := completionAnchor(path, s.Offset)
	return err == nil && anchor == s.Anchor
}

func (s *claudeTaskScan) unchangedBefore(offset int64, anchor string) bool {
	current, err := completionAnchor(s.Path, offset)
	return err == nil && current == anchor
}

// A reader sees one whole scan or the one before it. A failed save only
// makes the next process read more.
func (c *Claude) saveTaskScan(scan *claudeTaskScan) {
	// Read after the scan: Claude only appends, so bytes it has read are not rewritten in place.
	anchor, err := completionAnchor(scan.Path, scan.Offset)
	if err != nil {
		return
	}
	scan.Anchor = anchor
	encoded, err := json.Marshal(scan)
	if err != nil {
		return
	}
	dir := c.taskStateDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	c.taskPrune.Do(func() { pruneTaskStates(dir) })
	temp, err := os.CreateTemp(dir, ".scan-*")
	if err != nil {
		return
	}
	_, writeErr := temp.Write(encoded)
	closeErr := temp.Close()
	if writeErr != nil || closeErr != nil || os.Rename(temp.Name(), c.taskStatePath(scan.Path)) != nil {
		_ = os.Remove(temp.Name())
	}
}

func pruneTaskStates(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".scan-") {
			if info, err := entry.Info(); err == nil && time.Since(info.ModTime()) > time.Hour {
				_ = os.Remove(filepath.Join(dir, entry.Name()))
			}
			continue
		}
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		file := filepath.Join(dir, entry.Name())
		raw, err := os.ReadFile(file)
		if err != nil {
			continue
		}
		var saved struct {
			Path string `json:"path"`
		}
		if json.Unmarshal(raw, &saved) != nil || saved.Path == "" {
			_ = os.Remove(file)
			continue
		}
		if _, err := os.Stat(saved.Path); os.IsNotExist(err) {
			_ = os.Remove(file)
		}
	}
}

func skipJSONLRecord(path string, offset int64) (int64, bool) {
	file, err := os.Open(path)
	if err != nil {
		return offset, false
	}
	defer file.Close()
	chunk := make([]byte, 1<<20)
	for at := offset; ; {
		n, err := file.ReadAt(chunk, at)
		if index := bytes.IndexByte(chunk[:n], '\n'); index >= 0 {
			return at + int64(index) + 1, true
		}
		at += int64(n)
		if err == io.EOF || n == 0 {
			return offset, false
		}
		if err != nil {
			return offset, false
		}
	}
}

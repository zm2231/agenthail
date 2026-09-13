package surfaces

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	codexStatusReadChunk = int64(64 << 10)
	codexStatusReadLimit = int64(4 << 20)
	codexBusyFreshness   = 15 * time.Minute
)

func (c *Codex) reconcileLocalStatus(session *surface.Session) {
	path := codexTranscriptPath(session)
	if path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	session.Transcript = path
	session.HasLocal = true
	status := codexTranscriptStatus(path, info.ModTime(), time.Now())
	if status != surface.StatusUnknown && (session.Status == surface.StatusUnknown || session.Status == surface.SessionStatus("notLoaded")) {
		session.Status = status
	}
	if status == surface.StatusBusy && info.ModTime().After(session.LastActive) {
		session.LastActive = info.ModTime()
	}
}

func codexTranscriptStatus(path string, modified, now time.Time) surface.SessionStatus {
	file, err := os.Open(path)
	if err != nil {
		return surface.StatusUnknown
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return surface.StatusUnknown
	}

	var tail []byte
	for read := int64(0); read < info.Size() && read < codexStatusReadLimit; {
		chunk := codexStatusReadChunk
		if remaining := info.Size() - read; chunk > remaining {
			chunk = remaining
		}
		if remaining := codexStatusReadLimit - read; chunk > remaining {
			chunk = remaining
		}
		start := info.Size() - read - chunk
		part := make([]byte, chunk)
		if _, err := file.ReadAt(part, start); err != nil && err != io.EOF {
			return surface.StatusUnknown
		}
		tail = append(part, tail...)
		read += chunk
		lines := bytes.Split(tail, []byte{'\n'})
		for index := len(lines) - 1; index >= 0; index-- {
			var record struct {
				Type    string `json:"type"`
				Payload struct {
					Type string `json:"type"`
				} `json:"payload"`
			}
			if json.Unmarshal(lines[index], &record) != nil || record.Type != "event_msg" {
				continue
			}
			switch record.Payload.Type {
			case "task_complete", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted":
				return surface.StatusIdle
			case "task_started":
				if !modified.IsZero() && now.Sub(modified) <= codexBusyFreshness {
					return surface.StatusBusy
				}
				return surface.StatusUnknown
			}
		}
		if start == 0 {
			break
		}
		if newline := bytes.IndexByte(tail, '\n'); newline >= 0 {
			tail = tail[newline+1:]
		}
	}
	return surface.StatusUnknown
}

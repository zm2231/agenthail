package surfaces

import (
	"bytes"
	"context"
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
		session.Transcript = ""
		return
	}
	session.Transcript = path
	session.HasLocal = true
	applyCodexTranscriptStatus(session, info)
}

// applyCodexTranscriptStatus lets the transcript's latest task lifecycle
// override the provider status. List and LocalStatus share this rule so the
// catalog's discovery and status passes never disagree. A working session
// carries the open turn's activity even after the rollout has gone quiet
// for longer than the transcript alone counts as busy, since the runtime
// still reports the turn running.
func applyCodexTranscriptStatus(session *surface.Session, info os.FileInfo) {
	status, activity := codexTranscriptTurn(session.Transcript, info.ModTime(), time.Now())
	session.Activity = nil
	if status != surface.StatusUnknown {
		session.Status = status
		advanceBusyActivity(session, info.ModTime())
	}
	if session.Status == surface.StatusBusy && session.WaitingOn == nil {
		session.Activity = activity
	}
}

// LocalStatusFiles watches the pending model record even before the
// thread has a rollout, since a model can be chosen for a thread with no
// turns yet.
func (c *Codex) LocalStatusFiles(session surface.Session) []string {
	files := []string{codexPendingModelPath(session.ID)}
	if session.Transcript != "" {
		files = append(files, session.Transcript)
	}
	return files
}

func (c *Codex) LocalStatus(_ context.Context, session surface.Session) (surface.Session, error) {
	applyCodexPendingModel(&session)
	if session.Transcript == "" {
		return session, nil
	}
	info, err := os.Stat(session.Transcript)
	if err != nil {
		return session, err
	}
	applyCodexTranscriptStatus(&session, info)
	return session, nil
}

// codexTranscriptTurn reads the rollout backwards to its latest task
// lifecycle event. An open turn is busy while the rollout was written within
// codexBusyFreshness and its activity is returned either way: when it
// started, its newest record (or the rollout's last write when records carry
// no timestamp), and the newest tool call with no output yet.
func codexTranscriptTurn(path string, modified, now time.Time) (surface.SessionStatus, *surface.Activity) {
	file, err := os.Open(path)
	if err != nil {
		return surface.StatusUnknown, nil
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return surface.StatusUnknown, nil
	}

	var lastAt time.Time
	var tool *surface.RunningTool
	answered := map[string]bool{}
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
			return surface.StatusUnknown, nil
		}
		tail = append(part, tail...)
		read += chunk
		lines := bytes.Split(tail, []byte{'\n'})
		// The first line may continue in the chunk before; it is read with
		// that chunk unless the file starts here.
		first := 1
		if start == 0 {
			first = 0
		}
		for index := len(lines) - 1; index >= first; index-- {
			var record struct {
				Timestamp string `json:"timestamp"`
				Type      string `json:"type"`
				Payload   struct {
					Type   string `json:"type"`
					Name   string `json:"name"`
					CallID string `json:"call_id"`
				} `json:"payload"`
			}
			if json.Unmarshal(lines[index], &record) != nil {
				continue
			}
			at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
			if lastAt.IsZero() {
				lastAt = at
			}
			switch record.Type {
			case "response_item":
				switch record.Payload.Type {
				case "function_call_output", "custom_tool_call_output":
					answered[record.Payload.CallID] = true
				case "function_call", "custom_tool_call":
					if tool == nil && !answered[record.Payload.CallID] {
						tool = &surface.RunningTool{Name: record.Payload.Name, StartedAt: at}
					}
				}
			case "event_msg":
				switch record.Payload.Type {
				case "task_complete", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted":
					return surface.StatusIdle, nil
				case "task_started":
					activity := &surface.Activity{BusySince: at, LastProgressAt: lastAt, Tool: tool}
					if activity.LastProgressAt.IsZero() {
						activity.LastProgressAt = modified
					}
					if !modified.IsZero() && now.Sub(modified) <= codexBusyFreshness {
						return surface.StatusBusy, activity
					}
					return surface.StatusUnknown, activity
				}
			}
		}
		if start == 0 {
			break
		}
		if newline := bytes.IndexByte(tail, '\n'); newline >= 0 {
			tail = tail[:newline]
		}
	}
	return surface.StatusUnknown, nil
}

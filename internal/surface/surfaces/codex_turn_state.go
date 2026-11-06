package surfaces

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexLiveTurnRecord struct {
	Timestamp string `json:"timestamp"`
	Type      string `json:"type"`
	Payload   struct {
		Type   string `json:"type"`
		Name   string `json:"name"`
		CallID string `json:"call_id"`
		TurnID string `json:"turn_id"`
		Error  any    `json:"error"`
	} `json:"payload"`
}

type codexPendingCall struct {
	id   string
	tool surface.RunningTool
}

type codexLiveTurn struct {
	known     bool
	open      bool
	startedAt time.Time
	lastAt    time.Time
	pending   []codexPendingCall
	stop      *surface.TurnStop
}

func parseCodexLiveTurnRecord(line []byte) (codexLiveTurnRecord, time.Time, bool) {
	var record codexLiveTurnRecord
	if json.Unmarshal(line, &record) != nil {
		return record, time.Time{}, false
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	return record, at, true
}

func codexLifecycle(record codexLiveTurnRecord) (started, ended bool) {
	if record.Type != "event_msg" {
		return false, false
	}
	switch record.Payload.Type {
	case "task_started":
		return true, false
	case "task_complete", "task_cancelled", "task_canceled", "task_aborted", "turn_aborted":
		return false, true
	}
	return false, false
}

func (t *codexLiveTurn) apply(line []byte) {
	record, at, ok := parseCodexLiveTurnRecord(line)
	if !ok {
		return
	}
	if !at.IsZero() {
		t.lastAt = at
	}
	if started, ended := codexLifecycle(record); started || ended {
		t.known, t.open, t.pending, t.stop = true, started, nil, nil
		if started {
			t.startedAt = at
		} else if record.Payload.Error != nil {
			t.stop = codexFailureStop(record.Payload.TurnID, at, record.Payload.Error)
		}
		return
	}
	if !t.open || record.Type != "response_item" {
		return
	}
	switch record.Payload.Type {
	case "function_call", "custom_tool_call":
		t.pending = append(t.pending, codexPendingCall{id: record.Payload.CallID, tool: surface.RunningTool{Name: record.Payload.Name, StartedAt: at}})
	case "function_call_output", "custom_tool_call_output":
		for i, call := range t.pending {
			if call.id == record.Payload.CallID {
				t.pending = append(t.pending[:i], t.pending[i+1:]...)
				break
			}
		}
	}
}

func (t codexLiveTurn) status(modified, now time.Time) (surface.SessionStatus, *surface.Activity) {
	if !t.known {
		return surface.StatusUnknown, nil
	}
	if !t.open {
		return surface.StatusIdle, nil
	}
	activity := &surface.Activity{BusySince: t.startedAt, LastProgressAt: t.lastAt}
	if activity.LastProgressAt.IsZero() {
		activity.LastProgressAt = modified
	}
	if count := len(t.pending); count > 0 {
		tool := t.pending[count-1].tool
		activity.Tool = &tool
	}
	if !modified.IsZero() && now.Sub(modified) <= codexBusyFreshness {
		return surface.StatusBusy, activity
	}
	return surface.StatusUnknown, activity
}

type codexLifecycleScan struct {
	offset int64
	found  bool
	newest time.Time
	read   int64
}

func lastCodexLifecycle(file io.ReaderAt, size, limit int64) codexLifecycleScan {
	var scan codexLifecycleScan
	scan.read = scanJSONLBackward(file, size, limit, func(line []byte, offset int64) bool {
		record, recordAt, ok := parseCodexLiveTurnRecord(line)
		if !ok {
			return true
		}
		if scan.newest.IsZero() {
			scan.newest = recordAt
		}
		if started, ended := codexLifecycle(record); started || ended {
			scan.offset, scan.found = offset, true
			return false
		}
		return true
	})
	return scan
}

type codexLiveTurnState struct {
	path       string
	cursor     transcriptCursor
	turn       codexLiveTurn
	deep       bool
	read       bool
	lastActive time.Time
}

type codexLiveTurnRead struct {
	status   surface.SessionStatus
	activity *surface.Activity
	newest   time.Time
	stop     *surface.TurnStop
}

// Only a thread Codex runs or has loaded is read back past codexStatusReadLimit.
func (c *Codex) liveTurn(ctx context.Context, sessionID, path string, info os.FileInfo, deep bool, now time.Time) codexLiveTurnRead {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	if c.turnState == nil {
		c.turnState = map[string]*codexLiveTurnState{}
	}
	state := c.turnState[sessionID]
	if state == nil {
		state = &codexLiveTurnState{}
		c.turnState[sessionID] = state
	}
	if state.path != path {
		state.path, state.cursor = path, transcriptCursor{}
	}
	state.read = true
	if state.cursor.reset(info) || (deep && !state.deep && !state.turn.known) {
		state.turn, state.deep = codexLiveTurn{}, deep
		file, err := os.Open(path)
		if err != nil {
			state.cursor = transcriptCursor{}
			return codexLiveTurnRead{status: surface.StatusUnknown}
		}
		limit := codexStatusReadLimit
		if deep {
			limit = info.Size()
		}
		scan := lastCodexLifecycle(file, info.Size(), limit)
		file.Close()
		state.turn.lastAt = scan.newest
		state.cursor.offset = info.Size()
		if scan.found {
			state.cursor.offset = scan.offset
		}
	}
	if offset, err := scanAppendedJSONL(ctx, path, state.cursor.offset, maxCodexTranscriptRecordBytes, func(line []byte) error {
		state.turn.apply(line)
		return nil
	}); err == nil {
		state.cursor.offset = offset
	}
	status, activity := state.turn.status(info.ModTime(), now)
	return codexLiveTurnRead{status: status, activity: activity, newest: state.turn.lastAt, stop: state.turn.stop}
}

// The stored value outlives a truncated or replaced rollout.
func (c *Codex) keepLastActive(sessionID string, at time.Time) time.Time {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	state := c.turnState[sessionID]
	if state == nil {
		return at
	}
	if at.After(state.lastActive) {
		state.lastActive = at
	}
	return state.lastActive
}

func (c *Codex) forgetUnreadTurns() {
	c.turnMu.Lock()
	defer c.turnMu.Unlock()
	for id, state := range c.turnState {
		if !state.read {
			delete(c.turnState, id)
			continue
		}
		state.read = false
	}
}

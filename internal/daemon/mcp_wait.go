package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	mcpWaitDefaultSec = 600
	mcpWaitMaxSec     = 3600
	// Measured on Claude 2.1.318: with no timeout configured, an MCP tool call fails at 60 s.
	mcpWaitUnhostedClaudeSec = 45
	mcpWaitMaxTasks          = 32
	taskWaitReread           = 30 * time.Second
)

type mcpWaitInput struct {
	Tasks      []string `json:"tasks" jsonschema:"The tasks to wait on: taskIds, child session ids or @aliases."`
	TimeoutSec *int     `json:"timeoutSec,omitempty" jsonschema:"Seconds to wait. Default 600, at most 3600; 0 only looks."`
	Cursor     string   `json:"cursor,omitempty" jsonschema:"The cursor the last wait returned, so what it already told you isn't repeated."`
}

type mcpWaitItem struct {
	Type       string          `json:"type"`
	TaskID     int64           `json:"taskId"`
	Child      string          `json:"child"`
	Task       *mcpTaskResult  `json:"task,omitempty"`
	RequestID  string          `json:"requestId,omitempty"`
	Kind       string          `json:"kind,omitempty"`
	Tool       string          `json:"tool,omitempty"`
	Prompt     string          `json:"prompt,omitempty"`
	Clipped    bool            `json:"clipped,omitempty"`
	Ceiling    json.RawMessage `json:"ceiling,omitempty"`
	GoalStatus string          `json:"goalStatus,omitempty"`
	Reason     string          `json:"reason,omitempty"`
	Next       string          `json:"next,omitempty"`

	key     string
	task    registry.Task
	waiting *surface.Waiting
}

type mcpWaitRunning struct {
	TaskID int64  `json:"taskId"`
	Child  string `json:"child"`
}

type mcpWaitOutput struct {
	Ready   []mcpWaitItem    `json:"ready"`
	Running []mcpWaitRunning `json:"running"`
	Cursor  string           `json:"cursor"`
	Next    string           `json:"next,omitempty"`
}

type taskWaitState struct {
	ready   []mcpWaitItem
	running []registry.Task
	current map[string]bool
}

func (d *Daemon) mcpWait(ctx context.Context, caller *mcpCaller, input mcpWaitInput) (any, error) {
	refs, timeout, err := mcpWaitRequest(caller, input)
	if err != nil {
		return nil, err
	}
	seen, err := decodeWaitCursor(input.Cursor)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(timeout)
	for {
		var after uint64
		if d.events != nil {
			after = d.events.cursor()
		}
		state, err := d.readTaskWait(ctx, caller, refs, seen)
		if err != nil {
			return nil, err
		}
		if d.taskWaitRead != nil {
			d.taskWaitRead()
		}
		if len(state.ready) > 0 || len(state.running) == 0 {
			return d.finishTaskWait(caller, state, seen), nil
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			break
		}
		if err := d.awaitTaskChange(ctx, after, state.running, remaining); err != nil {
			return nil, err
		}
	}
	if timeout > 0 {
		if err := d.armTaskWakes(caller, refs); err != nil {
			return nil, err
		}
	}
	// A task that settled before its wake was armed is only seen by this read.
	state, err := d.readTaskWait(ctx, caller, refs, seen)
	if err != nil {
		return nil, err
	}
	return d.finishTaskWait(caller, state, seen), nil
}

func mcpWaitRequest(caller *mcpCaller, input mcpWaitInput) ([]mcpResultInput, time.Duration, error) {
	if len(input.Tasks) == 0 {
		return nil, 0, refuse("invalid", "tasks is required.")
	}
	if len(input.Tasks) > mcpWaitMaxTasks {
		return nil, 0, refuse("invalid", fmt.Sprintf("Wait on at most %d tasks at once.", mcpWaitMaxTasks))
	}
	seconds := mcpWaitDefaultSec
	if input.TimeoutSec != nil {
		seconds = *input.TimeoutSec
	}
	if seconds < 0 {
		return nil, 0, refuse("invalid", "timeoutSec can't be negative.")
	}
	seconds = min(seconds, mcpWaitLimitSec(caller.session))
	refs := make([]mcpResultInput, 0, len(input.Tasks))
	for _, ref := range input.Tasks {
		ref = strings.TrimSpace(ref)
		if id, err := strconv.ParseInt(ref, 10, 64); err == nil {
			refs = append(refs, mcpResultInput{TaskID: id})
		} else {
			refs = append(refs, mcpResultInput{Target: ref})
		}
	}
	return refs, time.Duration(seconds) * time.Second, nil
}

// A Claude session Agenthail doesn't host keeps Claude's own limit on a tool call.
func mcpWaitLimitSec(session surface.Session) int {
	if session.Surface == surface.KindClaude && session.RuntimeKind != surface.RuntimeClaudeHost {
		return mcpWaitUnhostedClaudeSec
	}
	return mcpWaitMaxSec
}

func decodeWaitCursor(cursor string) (map[string]bool, error) {
	seen := map[string]bool{}
	if cursor == "" {
		return seen, nil
	}
	// A wait's cursor is part of its output, so it can't be longer than that.
	if len(cursor) > mcpOutputLimit {
		return nil, refuse("invalid", "That cursor isn't one a wait returned.")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	var keys []string
	if err == nil {
		err = json.Unmarshal(decoded, &keys)
	}
	if err != nil {
		return nil, refuse("invalid", "That cursor isn't one a wait returned.")
	}
	for _, key := range keys {
		seen[key] = true
	}
	return seen, nil
}

func (d *Daemon) readTaskWait(ctx context.Context, caller *mcpCaller, refs []mcpResultInput, seen map[string]bool) (taskWaitState, error) {
	state := taskWaitState{current: map[string]bool{}}
	counted := map[int64]bool{}
	for _, ref := range refs {
		task, err := d.mcpReadableTask(caller, ref)
		if err != nil {
			return state, err
		}
		if counted[task.ID] {
			continue
		}
		counted[task.ID] = true
		items := []mcpWaitItem{}
		if task.State == registry.TaskRunning {
			state.running = append(state.running, task)
			items = d.taskAttention(ctx, task)
		} else {
			items = append(items, mcpWaitItem{Type: "settled", TaskID: task.ID, Child: task.Child, key: fmt.Sprintf("s:%d", task.ID), task: task})
		}
		for _, item := range items {
			state.current[item.key] = true
			if !seen[item.key] {
				state.ready = append(state.ready, item)
			}
		}
	}
	return state, nil
}

func (d *Daemon) taskAttention(ctx context.Context, task registry.Task) []mcpWaitItem {
	session, err := d.Registry.Session(task.Child)
	if err != nil {
		return nil
	}
	items := d.taskRequests(ctx, task, session)
	adapter, err := d.adapters().For(session)
	if err != nil || !surface.EffectiveCapabilities(adapter, session).Goal {
		return items
	}
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	goal, err := adapter.GoalGet(readCtx, session)
	if err != nil || goal == nil || goal.CreatedAt.IsZero() || !goalStopped(goal.Status) {
		return items
	}
	return append(items, mcpWaitItem{
		Type: "goal_stopped", TaskID: task.ID, Child: task.Child, GoalStatus: goal.Status, Reason: goal.Reason,
		Next: "Resume it with agenthail_goal, or end the task with agenthail_interrupt and agenthail_archive.",
		key:  "g:" + goalStopNoticeKey(task.Child, goal),
	})
}

func (d *Daemon) taskRequests(ctx context.Context, task registry.Task, session *surface.Session) []mcpWaitItem {
	waiting, err := d.Registry.SessionWaiting(task.Child)
	if err != nil || waiting == nil {
		return nil
	}
	ceiling, err := d.Registry.SessionCeiling(task.Child)
	if err != nil {
		ceiling = nil
	}
	item := func(id, kind string) mcpWaitItem {
		return mcpWaitItem{
			Type: "request", TaskID: task.ID, Child: task.Child, RequestID: id, Kind: kind, Ceiling: ceiling,
			Next: "Answer it with agenthail_answer, or leave it for the person.",
			key:  "r:" + task.Child + ":" + id, waiting: waiting,
		}
	}
	requests, _, err := d.openRequests(ctx, session)
	if err != nil || len(requests) == 0 {
		return []mcpWaitItem{item(waiting.ID, string(waiting.Kind))}
	}
	items := make([]mcpWaitItem, 0, len(requests))
	for _, request := range requests {
		view := requestView(request, mcpItemTextLimit)
		row := item(view.ID, view.Kind)
		row.Tool, row.Prompt, row.Clipped = view.Tool, view.Prompt, view.Clipped
		items = append(items, row)
	}
	return items
}

func (d *Daemon) awaitTaskChange(ctx context.Context, after uint64, running []registry.Task, remaining time.Duration) error {
	timer := time.NewTimer(min(remaining, taskWaitReread))
	defer timer.Stop()
	var follower *eventFollower
	var wake <-chan struct{}
	if d.events != nil {
		var backlog []apiEvent
		var reset bool
		backlog, follower, reset = d.events.subscribe(after)
		defer follower.Cancel()
		if reset || concernsTask(backlog, running) {
			return nil
		}
		wake = follower.Wake
	}
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-timer.C:
			return nil
		case _, open := <-wake:
			if !open {
				return nil
			}
			if events, gap := follower.read(); gap || concernsTask(events, running) {
				return nil
			}
		}
	}
}

func concernsTask(events []apiEvent, running []registry.Task) bool {
	for _, event := range events {
		if event.Type != taskEventSettled && event.Type != taskEventRequest && event.Type != taskEventGoalStopped {
			continue
		}
		for _, task := range running {
			if task.Child == event.EntityID {
				return true
			}
		}
	}
	return false
}

func (d *Daemon) armTaskWakes(caller *mcpCaller, refs []mcpResultInput) error {
	for _, ref := range refs {
		task, err := d.mcpReadableTask(caller, ref)
		if err != nil {
			return err
		}
		if task.Parent != caller.session.ID {
			continue
		}
		if err := d.Registry.ArmTaskWake(task.ID); err != nil {
			return err
		}
	}
	return nil
}

// The side effects follow the trim, so what didn't fit stays unread for the next wait.
func (d *Daemon) finishTaskWait(caller *mcpCaller, state taskWaitState, seen map[string]bool) mcpWaitOutput {
	ready := d.fitWaitItems(state.ready)
	returned := map[string]bool{}
	for key := range seen {
		if state.current[key] {
			returned[key] = true
		}
	}
	for _, item := range ready {
		returned[item.key] = true
		d.noteWaitItemRead(caller, item)
	}
	keys := make([]string, 0, len(returned))
	for key := range returned {
		keys = append(keys, key)
	}
	encoded, _ := json.Marshal(keys)
	output := mcpWaitOutput{Ready: ready, Running: []mcpWaitRunning{}, Cursor: base64.RawURLEncoding.EncodeToString(encoded)}
	for _, task := range state.running {
		output.Running = append(output.Running, mcpWaitRunning{TaskID: task.ID, Child: task.Child})
	}
	if len(output.Running) > 0 {
		output.Next = "Wait again with this cursor, or end your turn: you get one notice when a task you started settles after a wait timed out."
	}
	if len(ready) < len(state.ready) {
		output.Next = "More is ready than fits. Wait again with this cursor to read the rest."
	}
	return output
}

func (d *Daemon) fitWaitItems(items []mcpWaitItem) []mcpWaitItem {
	ready := make([]mcpWaitItem, 0, len(items))
	if len(items) == 0 {
		return ready
	}
	share := mcpOutputLimit / (2 * len(items))
	used := 0
	for _, item := range items {
		if item.Type == "settled" {
			fresh := d.refreshTaskLatest(context.Background(), item.task)
			result := clippedTaskResult(fresh, share)
			item.Task = &result
		}
		if len(item.Prompt) > share {
			item.Prompt, item.Clipped = clipUTF8(item.Prompt, share), true
		}
		encoded, _ := json.Marshal(item)
		if used+len(encoded) > mcpOutputLimit*3/4 && len(ready) > 0 {
			break
		}
		used += len(encoded)
		ready = append(ready, item)
	}
	return ready
}

func (d *Daemon) noteWaitItemRead(caller *mcpCaller, item mcpWaitItem) {
	switch item.Type {
	case "settled":
		if item.task.Parent == caller.session.ID {
			if err := d.clearTaskWake(item.task.ID); err != nil {
				d.log.Printf("clear wake of task %d: %s", item.task.ID, err)
			}
		}
	case "request":
		session, err := d.Registry.Session(item.Child)
		if err != nil || item.waiting == nil {
			return
		}
		if to, ok := d.noticeRecipient(*session); ok && to.sessionID == caller.session.ID {
			d.noteWaitReadRequest(item.Child, caller.session.ID, *item.waiting)
		}
	}
}

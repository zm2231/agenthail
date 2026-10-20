package surfaces

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var claudeTaskIDTag = regexp.MustCompile(`(?:<|\\u003c)task-id(?:>|\\u003e)(.*?)(?:<|\\u003c)/task-id`)

// claudeBackground reads the background tasks of a live session: from the host
// for a session it runs, otherwise from the transcript and the process's own
// status.
func (c *Claude) claudeBackground(ctx context.Context, sess *surface.Session) *surface.BackgroundTasks {
	if claudeHosted(sess) {
		if tasks, ok := c.hostBackground(ctx, sess); ok {
			return tasks
		}
	}
	return c.recordBackground(ctx, sess)
}

// hostBackground reads the tasks Claude last reported to the host. A host that
// predates task listing reports none, and ok is false.
func (c *Claude) hostBackground(ctx context.Context, sess *surface.Session) (*surface.BackgroundTasks, bool) {
	listed, ok := c.host.session(ctx, sess.ID, sess.PID)
	if !ok || listed.Tasks == nil {
		return nil, false
	}
	tasks := make([]surface.BackgroundTask, 0, len(listed.Tasks))
	for _, task := range listed.Tasks {
		tasks = append(tasks, surface.BackgroundTask{ID: task.ID, Kind: claudeTaskKind(task.Type), Description: task.Description, ParentID: task.ParentID, Ambient: task.Ambient, Since: task.Since.UTC()})
	}
	return backgroundOf(tasks, surface.BackgroundSourceHost), true
}

// claudeTaskKind names Claude's task types by what they are.
func claudeTaskKind(taskType string) string {
	switch taskType {
	case "local_bash":
		return surface.BackgroundShell
	case "local_agent", "remote_agent", "in_process_teammate":
		return surface.BackgroundAgent
	case "monitor_mcp", "monitor_ws":
		return surface.BackgroundMonitor
	default:
		return taskType
	}
}

// recordBackground reads a terminal session's tasks from its transcript: those
// its live process started and that have not ended. Claude's record says
// "shell" while a background shell runs, so a shell the transcript leaves open
// counts only then, and "shell" with no shell listed still counts.
func (c *Claude) recordBackground(ctx context.Context, sess *surface.Session) *surface.BackgroundTasks {
	record, live := c.liveRecord(*sess)
	if !live {
		return nil
	}
	shell := str(record, "status") == "shell"
	var started time.Time
	if at, ok := record["startedAt"].(float64); ok {
		started = time.UnixMilli(int64(at))
	}
	var tasks []surface.BackgroundTask
	if path := sess.Transcript; path != "" {
		for _, task := range c.transcriptTasks(ctx, path) {
			if task.Since.Before(started) || task.Kind == surface.BackgroundShell && !shell {
				continue
			}
			tasks = append(tasks, task)
		}
	}
	background := backgroundOf(tasks, surface.BackgroundSourceTranscript)
	if shell && !surface.BackgroundRunning(background) {
		if background == nil {
			background = &surface.BackgroundTasks{Source: surface.BackgroundSourceStatus}
		}
		background.Running = true
		if at, ok := record["statusUpdatedAt"].(float64); ok {
			background.Since = time.UnixMilli(int64(at)).UTC()
		}
	}
	return background
}

func backgroundOf(tasks []surface.BackgroundTask, source string) *surface.BackgroundTasks {
	if len(tasks) == 0 {
		return nil
	}
	background := &surface.BackgroundTasks{Tasks: tasks, Source: source}
	for _, task := range tasks {
		if task.Ambient {
			continue
		}
		background.Running = true
		if !task.Since.IsZero() && (background.Since.IsZero() || task.Since.Before(background.Since)) {
			background.Since = task.Since
		}
	}
	return background
}

// claudeTaskScan follows one transcript's background tasks as Claude appends
// to it: a Bash or Agent call run in the background, or a Monitor, starts a
// task its result names; a task notification or a TaskStop on the id ends it.
type claudeTaskScan struct {
	file    os.FileInfo
	offset  int64
	calls   map[string]surface.BackgroundTask
	running []surface.BackgroundTask
}

func (c *Claude) transcriptTasks(ctx context.Context, path string) []surface.BackgroundTask {
	c.tasksMu.Lock()
	defer c.tasksMu.Unlock()
	if c.taskScans == nil {
		c.taskScans = map[string]*claudeTaskScan{}
	}
	info, err := os.Stat(path)
	if err != nil {
		delete(c.taskScans, path)
		return nil
	}
	scan := c.taskScans[path]
	if scan == nil || !os.SameFile(scan.file, info) || info.Size() < scan.offset {
		scan = &claudeTaskScan{calls: map[string]surface.BackgroundTask{}}
		c.taskScans[path] = scan
	}
	scan.file = info
	next, err := scanAppendedJSONL(ctx, path, scan.offset, maxClaudeTranscriptRecordBytes, func(line []byte) error {
		scan.read(line)
		return nil
	})
	if err == nil {
		scan.offset = next
	}
	return append([]surface.BackgroundTask(nil), scan.running...)
}

func (s *claudeTaskScan) read(line []byte) {
	for _, match := range claudeTaskIDTag.FindAllSubmatch(line, -1) {
		s.end(string(match[1]))
	}
	if !bytes.Contains(line, []byte(`"tool_use"`)) && !bytes.Contains(line, []byte(`"toolUseResult"`)) {
		return
	}
	var record struct {
		Timestamp string `json:"timestamp"`
		Message   struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
		Result struct {
			BackgroundTaskID string `json:"backgroundTaskId"`
			AgentID          string `json:"agentId"`
			IsAsync          bool   `json:"isAsync"`
			TaskID           string `json:"taskId"`
		} `json:"toolUseResult"`
	}
	if json.Unmarshal(line, &record) != nil {
		return
	}
	var blocks []struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		Name      string `json:"name"`
		ToolUseID string `json:"tool_use_id"`
		Input     struct {
			Description string `json:"description"`
			Command     string `json:"command"`
			TaskID      string `json:"task_id"`
			ShellID     string `json:"shell_id"`
		} `json:"input"`
	}
	if json.Unmarshal(record.Message.Content, &blocks) != nil {
		return
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	for _, block := range blocks {
		switch block.Type {
		case "tool_use":
			description := strings.TrimSpace(block.Input.Description)
			if description == "" {
				description = strings.TrimSpace(block.Input.Command)
			}
			// The result says whether a call went to the background: a shell
			// can be moved there while it runs and an agent can be
			// asynchronous by its definition, so the input's flag is not the
			// mark.
			switch {
			case block.Name == "Bash":
				s.calls[block.ID] = surface.BackgroundTask{Kind: surface.BackgroundShell, Description: description}
			case block.Name == "Agent":
				s.calls[block.ID] = surface.BackgroundTask{Kind: surface.BackgroundAgent, Description: description}
			case block.Name == "Monitor":
				s.calls[block.ID] = surface.BackgroundTask{Kind: surface.BackgroundMonitor, Description: description}
			case block.Name == "TaskStop":
				s.end(block.Input.TaskID)
				s.end(block.Input.ShellID)
			}
		case "tool_result":
			task, ok := s.calls[block.ToolUseID]
			if !ok {
				continue
			}
			delete(s.calls, block.ToolUseID)
			switch task.Kind {
			case surface.BackgroundShell:
				task.ID = record.Result.BackgroundTaskID
			case surface.BackgroundAgent:
				if record.Result.IsAsync {
					task.ID = record.Result.AgentID
				}
			case surface.BackgroundMonitor:
				task.ID = record.Result.TaskID
			}
			if task.ID == "" {
				continue
			}
			task.Since = at.UTC()
			s.end(task.ID)
			s.running = append(s.running, task)
		}
	}
}

func (s *claudeTaskScan) end(id string) {
	if id == "" {
		return
	}
	for index, task := range s.running {
		if task.ID == id {
			s.running = append(s.running[:index], s.running[index+1:]...)
			return
		}
	}
}

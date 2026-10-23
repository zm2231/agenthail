package surfaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

// claudeTask is what a transcript shows of one task its session made.
type claudeTask struct {
	subject string
	deleted bool
}

type claudeTaskCall struct {
	name    string
	subject string
	taskID  string
	status  string
}

// claudePlanState follows a transcript's task calls. Claude keeps the list
// itself in <config root>/tasks/<list id>/, so a new task result marks the
// list for one read on the next observation, and scans that bring none reuse
// it. tasks is every task the transcript shows this session creating, which
// the list must agree with to count as the session's own.
type claudePlanState struct {
	calls map[string]claudeTaskCall
	tasks map[string]claudeTask
	stale bool
	plan  *surface.Plan
}

func (s *claudePlanState) apply(record claudeRecord, line []byte) {
	blocks, _ := record.Message.Content.([]any)
	switch record.Type {
	case "assistant":
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if str(block, "type") != "tool_use" {
				continue
			}
			input, _ := block["input"].(map[string]any)
			switch name := str(block, "name"); name {
			case "TaskCreate", "TaskUpdate":
				if s.calls == nil {
					s.calls = map[string]claudeTaskCall{}
				}
				s.calls[str(block, "id")] = claudeTaskCall{name: name, subject: str(input, "subject"), taskID: str(input, "taskId"), status: str(input, "status")}
			case "TodoWrite":
				s.tasks, s.stale = nil, false
				s.plan = claudeTodoPlan(input)
			}
		}
	case "user":
		if len(s.calls) == 0 {
			return
		}
		ids := []string{}
		if record.Message.ToolUseID != "" {
			ids = append(ids, record.Message.ToolUseID)
		}
		for _, raw := range blocks {
			block, _ := raw.(map[string]any)
			if str(block, "type") == "tool_result" {
				ids = append(ids, str(block, "tool_use_id"))
			}
		}
		for _, id := range ids {
			call, found := s.calls[id]
			if !found {
				continue
			}
			delete(s.calls, id)
			if s.record(call, line) {
				s.stale = true
			}
		}
	}
}

// record applies one task result to what the transcript shows, and reports
// whether it was one.
func (s *claudePlanState) record(call claudeTaskCall, line []byte) bool {
	var result struct {
		ToolUseResult struct {
			Task *struct {
				ID      string `json:"id"`
				Subject string `json:"subject"`
			} `json:"task"`
			Success *bool  `json:"success"`
			TaskID  string `json:"taskId"`
		} `json:"toolUseResult"`
	}
	if json.Unmarshal(line, &result) != nil {
		return false
	}
	if s.tasks == nil {
		s.tasks = map[string]claudeTask{}
	}
	switch call.name {
	case "TaskCreate":
		if task := result.ToolUseResult.Task; task != nil && task.ID != "" {
			s.tasks[task.ID] = claudeTask{subject: task.Subject}
			return true
		}
	case "TaskUpdate":
		id := result.ToolUseResult.TaskID
		if result.ToolUseResult.Success == nil || !*result.ToolUseResult.Success || id == "" {
			return false
		}
		task, known := s.tasks[id]
		if !known {
			return true
		}
		if call.subject != "" {
			task.subject = call.subject
		}
		task.deleted = call.status == "deleted"
		s.tasks[id] = task
		return true
	}
	return false
}

func claudeTodoPlan(input map[string]any) *surface.Plan {
	todos, _ := input["todos"].([]any)
	plan := &surface.Plan{}
	for _, raw := range todos {
		todo, _ := raw.(map[string]any)
		plan.Total++
		switch str(todo, "status") {
		case "completed":
			plan.Done++
		case "in_progress":
			if plan.Current == "" {
				plan.Current = planStep(str(todo, "activeForm"), str(todo, "content"))
			}
		}
	}
	if plan.Total == 0 {
		return nil
	}
	return plan
}

// resolve reads the task list when a task result since the last read asks
// for it. The list in the directory named for the session is its own only
// when it agrees with the transcript: every task the session created and
// still has is there under the same subject, every one it deleted is gone,
// and it created at least one. A session whose list lives elsewhere, as with
// CLAUDE_CODE_TASK_LIST_ID or an agent team, shows no plan.
func (s *claudePlanState) resolve(transcript string) {
	if !s.stale {
		return
	}
	s.stale = false
	s.plan = readClaudeTaskPlan(transcript, s.tasks)
}

var claudeTaskListName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

var readClaudeTaskPlanFiles func(dir string)

func readClaudeTaskPlan(transcript string, known map[string]claudeTask) *surface.Plan {
	live := 0
	for _, task := range known {
		if !task.deleted {
			live++
		}
	}
	if live == 0 {
		return nil
	}
	session := strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	dir := filepath.Join(claudeTranscriptRoot(transcript), "tasks", claudeTaskListName.ReplaceAllString(session, "-"))
	if readClaudeTaskPlanFiles != nil {
		readClaudeTaskPlanFiles(dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	plan := &surface.Plan{}
	found := 0
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".json") || strings.HasPrefix(name, ".") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil
		}
		var task struct {
			ID         string `json:"id"`
			Subject    string `json:"subject"`
			ActiveForm string `json:"activeForm"`
			Status     string `json:"status"`
		}
		if json.Unmarshal(data, &task) != nil {
			continue
		}
		if mine, ok := known[task.ID]; ok {
			if mine.deleted || task.Subject != mine.subject {
				return nil
			}
			found++
		}
		if task.Status == "deleted" {
			continue
		}
		plan.Total++
		switch task.Status {
		case "completed":
			plan.Done++
		case "in_progress":
			if plan.Current == "" {
				plan.Current = planStep(task.ActiveForm, task.Subject)
			}
		}
	}
	if found != live || plan.Total == 0 {
		return nil
	}
	return plan
}

func planStep(active, subject string) string {
	if strings.TrimSpace(active) != "" {
		return active
	}
	return subject
}

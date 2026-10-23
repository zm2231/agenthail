package surfaces

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/zm2231/agenthail/internal/surface"
)

type claudeTaskEvidence struct {
	ID      string
	Subject string
	Deleted bool
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
// it.
type claudePlanState struct {
	calls    map[string]claudeTaskCall
	evidence *claudeTaskEvidence
	stale    bool
	plan     *surface.Plan
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
				s.evidence, s.stale = nil, false
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
			evidence := claudeTaskResult(call, line)
			if evidence == nil {
				continue
			}
			s.evidence = evidence
			s.stale = true
		}
	}
}

func claudeTaskResult(call claudeTaskCall, line []byte) *claudeTaskEvidence {
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
		return nil
	}
	switch call.name {
	case "TaskCreate":
		if task := result.ToolUseResult.Task; task != nil && task.ID != "" {
			return &claudeTaskEvidence{ID: task.ID, Subject: task.Subject}
		}
	case "TaskUpdate":
		if result.ToolUseResult.Success != nil && *result.ToolUseResult.Success && result.ToolUseResult.TaskID != "" {
			return &claudeTaskEvidence{ID: result.ToolUseResult.TaskID, Deleted: call.status == "deleted"}
		}
	}
	return nil
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
// for it. The list is the session's own only when the newest task call this
// transcript shows agrees with it; a session whose list lives elsewhere, as
// with CLAUDE_CODE_TASK_LIST_ID or an agent team, shows no plan.
func (s *claudePlanState) resolve(transcript string) {
	if !s.stale {
		return
	}
	s.stale = false
	s.plan = readClaudeTaskPlan(transcript, s.evidence)
}

var claudeTaskListName = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

var readClaudeTaskPlanFiles func(dir string)

func readClaudeTaskPlan(transcript string, evidence *claudeTaskEvidence) *surface.Plan {
	if evidence == nil {
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
	attributed := evidence.Deleted
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
		if task.ID == evidence.ID {
			if evidence.Deleted || evidence.Subject != "" && task.Subject != evidence.Subject {
				return nil
			}
			attributed = true
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
	if !attributed || plan.Total == 0 {
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

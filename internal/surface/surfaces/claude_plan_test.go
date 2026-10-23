package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

const planConversation = "6f1c2d3e-0000-4000-8000-000000000001"

func planTranscript(t *testing.T, lines string) (root, path string) {
	t.Helper()
	root = t.TempDir()
	path = filepath.Join(root, "projects", "-work-app", planConversation+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(lines)+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return root, path
}

func writeTasks(t *testing.T, root, list string, tasks ...map[string]any) {
	t.Helper()
	dir := filepath.Join(root, "tasks", list)
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	for _, task := range tasks {
		data, _ := json.Marshal(task)
		if err := os.WriteFile(filepath.Join(dir, task["id"].(string)+".json"), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func task(id, subject, status string) map[string]any {
	return map[string]any{"id": id, "subject": subject, "activeForm": "Working on " + subject, "status": status, "blocks": []any{}, "blockedBy": []any{}}
}

const planCreates = `
{"type":"user","uuid":"u1","timestamp":"2026-10-23T10:00:00Z","message":{"role":"user","content":"plan it"}}
{"type":"assistant","uuid":"a1","timestamp":"2026-10-23T10:00:01Z","message":{"id":"m1","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_c1","name":"TaskCreate","input":{"subject":"Parse","description":"","activeForm":"Parsing"}}]}}
{"type":"user","uuid":"r1","timestamp":"2026-10-23T10:00:02Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_c1","content":"Task #1 created"}]},"toolUseResult":{"task":{"id":"1","subject":"Parse"}}}
{"type":"assistant","uuid":"a2","timestamp":"2026-10-23T10:00:03Z","message":{"id":"m2","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_c2","name":"TaskCreate","input":{"subject":"Render","description":"","activeForm":"Rendering"}}]}}
{"type":"user","uuid":"r2","timestamp":"2026-10-23T10:00:04Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_c2","content":"Task #2 created"}]},"toolUseResult":{"task":{"id":"2","subject":"Render"}}}
`

func planUpdate(id, toolID, status string) string {
	return `{"type":"assistant","uuid":"a-` + toolID + `","timestamp":"2026-10-23T10:01:00Z","message":{"id":"m-` + toolID + `","stop_reason":"tool_use","content":[{"type":"tool_use","id":"` + toolID + `","name":"TaskUpdate","input":{"taskId":"` + id + `","status":"` + status + `"}}]}}
{"type":"user","uuid":"r-` + toolID + `","timestamp":"2026-10-23T10:01:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + toolID + `","content":"Updated task #` + id + `"}]},"toolUseResult":{"success":true,"taskId":"` + id + `","updatedFields":["status"]}}
`
}

func countPlanReads(t *testing.T) *int {
	t.Helper()
	reads := 0
	readClaudeTaskPlanFiles = func(string) { reads++ }
	t.Cleanup(func() { readClaudeTaskPlanFiles = nil })
	return &reads
}

func observePlan(t *testing.T, claude *Claude, path string) *surface.Plan {
	t.Helper()
	session := &surface.Session{ID: planConversation, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, Transcript: path, HasLocal: true}
	observation, err := claude.Observe(context.Background(), session)
	if err != nil {
		t.Fatal(err)
	}
	return observation.Plan
}

func TestClaudePlanCountsTheSessionsOwnTaskList(t *testing.T) {
	root, path := planTranscript(t, planCreates+planUpdate("1", "toolu_u1", "in_progress"))
	writeTasks(t, root, planConversation, task("1", "Parse", "in_progress"), task("2", "Render", "pending"))
	claude := NewClaude(nil, t.TempDir())
	if plan := observePlan(t, claude, path); plan == nil || *plan != (surface.Plan{Done: 0, Total: 2, Current: "Working on Parse"}) {
		t.Fatalf("plan=%+v", plan)
	}
	writeTasks(t, root, planConversation, task("1", "Parse", "completed"), task("2", "Render", "in_progress"))
	appendTranscript(t, path, planUpdate("1", "toolu_u2", "completed")+planUpdate("2", "toolu_u3", "in_progress"))
	if plan := observePlan(t, claude, path); plan == nil || *plan != (surface.Plan{Done: 1, Total: 2, Current: "Working on Render"}) {
		t.Fatalf("after updates plan=%+v", plan)
	}
}

func TestClaudePlanReadsTheTaskListOnlyAfterATaskResult(t *testing.T) {
	reads := countPlanReads(t)
	root, path := planTranscript(t, planCreates)
	writeTasks(t, root, planConversation, task("1", "Parse", "pending"), task("2", "Render", "pending"))
	claude := NewClaude(nil, t.TempDir())
	for range 3 {
		if plan := observePlan(t, claude, path); plan == nil || plan.Total != 2 {
			t.Fatalf("plan=%+v", plan)
		}
	}
	if *reads != 1 {
		t.Fatalf("three observations with one batch of task results read the list %d times", *reads)
	}
	appendTranscript(t, path, `{"type":"assistant","uuid":"a9","timestamp":"2026-10-23T10:02:00Z","message":{"id":"m9","stop_reason":"end_turn","content":[{"type":"text","text":"done"}]}}`+"\n")
	observePlan(t, claude, path)
	if *reads != 1 {
		t.Fatalf("a record with no task result read the list again: %d reads", *reads)
	}
	appendTranscript(t, path, planUpdate("2", "toolu_u4", "in_progress"))
	observePlan(t, claude, path)
	observePlan(t, claude, path)
	if *reads != 2 {
		t.Fatalf("one new task result should read the list once more, got %d reads", *reads)
	}
}

func TestClaudePlanIsNotShownForAListItCannotAttribute(t *testing.T) {
	root, path := planTranscript(t, planCreates)
	claude := NewClaude(nil, t.TempDir())
	if plan := observePlan(t, claude, path); plan != nil {
		t.Fatalf("a list kept under another id (CLAUDE_CODE_TASK_LIST_ID or a team) showed plan=%+v", plan)
	}

	root, path = planTranscript(t, planCreates)
	writeTasks(t, root, planConversation, task("1", "Parse", "pending"), task("2", "Something else", "pending"))
	if plan := observePlan(t, NewClaude(nil, t.TempDir()), path); plan != nil {
		t.Fatalf("a list whose task differs from the one this session created showed plan=%+v", plan)
	}

	root, path = planTranscript(t, planCreates)
	writeTasks(t, root, planConversation, task("1", "Parse", "pending"))
	if plan := observePlan(t, NewClaude(nil, t.TempDir()), path); plan != nil {
		t.Fatalf("a list missing the task this session created showed plan=%+v", plan)
	}

	root, path = planTranscript(t, `{"type":"user","uuid":"u1","timestamp":"2026-10-23T10:00:00Z","message":{"role":"user","content":"hi"}}`)
	writeTasks(t, root, planConversation, task("1", "Parse", "pending"))
	if plan := observePlan(t, NewClaude(nil, t.TempDir()), path); plan != nil {
		t.Fatalf("a transcript with no task calls showed plan=%+v", plan)
	}
}

func TestClaudePlanDropsADeletedTaskAndFollowsTodoWrite(t *testing.T) {
	root, path := planTranscript(t, planCreates+planUpdate("2", "toolu_d", "deleted"))
	writeTasks(t, root, planConversation, task("1", "Parse", "completed"))
	claude := NewClaude(nil, t.TempDir())
	if plan := observePlan(t, claude, path); plan == nil || *plan != (surface.Plan{Done: 1, Total: 1}) {
		t.Fatalf("after deleting task 2 plan=%+v", plan)
	}
	appendTranscript(t, path, `{"type":"assistant","uuid":"a7","timestamp":"2026-10-23T10:03:00Z","message":{"id":"m7","stop_reason":"tool_use","content":[{"type":"tool_use","id":"toolu_t","name":"TodoWrite","input":{"todos":[{"content":"Trace","status":"completed","activeForm":"Tracing"},{"content":"Fix","status":"in_progress","activeForm":"Fixing"},{"content":"Ship","status":"pending","activeForm":"Shipping"}]}}]}}`+"\n")
	if plan := observePlan(t, claude, path); plan == nil || *plan != (surface.Plan{Done: 1, Total: 3, Current: "Fixing"}) {
		t.Fatalf("after TodoWrite plan=%+v", plan)
	}
}

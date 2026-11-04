package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type taskSurface struct {
	*daemonSurface
	goal    *surface.GoalState
	goalErr error
	onGoal  func()
}

func (s *taskSurface) GoalGet(context.Context, *surface.Session) (*surface.GoalState, error) {
	if s.onGoal != nil {
		s.onGoal()
	}
	return s.goal, s.goalErr
}

type taskFixture struct {
	d           *Daemon
	r           *registry.Registry
	surface     *taskSurface
	parent      *mcp.ClientSession
	parentToken string
	child       *mcp.ClientSession
	session     surface.Session
	task        registry.Task
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, base, parent, child := daemonFixture(t)
	base.caps = surface.Capabilities{Send: true, Goal: true}
	adapter := &taskSurface{daemonSurface: base}
	d.Surfaces = []surface.Surface{adapter}
	if err := r.RecordSessionOrigin(registry.OriginOf(child.ID, parent.ID)); err != nil {
		t.Fatal(err)
	}
	task, err := r.CreateTask(parent.ID, child.ID, "do the thing", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	server := mcpServer(t, d)
	parentToken := issueMCPToken(t, d, parent)
	return &taskFixture{d: d, r: r, surface: adapter, parent: mcpSession(t, server, parentToken), parentToken: parentToken, child: mcpSession(t, server, issueMCPToken(t, d, child)), session: child, task: task}
}

func taskCreator(t *testing.T) (*registry.Registry, *daemonSurface, *mcp.ClientSession, map[string]any) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	d, r, fake, parent, _ := daemonFixture(t)
	session := mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, parent))
	return r, fake, session, map[string]any{"surface": "codex", "cwd": t.TempDir(), "message": "do the thing", "task": true, "clientRequestId": "c1"}
}

func TestACreatedTaskIsRecordedAndAReplayReturnsTheSameOne(t *testing.T) {
	r, _, session, args := taskCreator(t)
	first, refused := callMCP(t, session, "agenthail_create", args)
	if refused || first["taskId"] == nil {
		t.Fatalf("first=%v", first)
	}
	again, refused := callMCP(t, session, "agenthail_create", args)
	if refused || again["taskId"] != first["taskId"] {
		t.Fatalf("again=%v first=%v", again, first)
	}
	child := first["session"].(map[string]any)["id"].(string)
	task, found, err := r.TaskForChild(child)
	if err != nil || !found || task.Parent != "from" || task.Text != "do the thing" || task.State != registry.TaskRunning {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
}

func TestAPlainCreateRecordsNoTask(t *testing.T) {
	r, _, session, args := taskCreator(t)
	delete(args, "task")
	out, refused := callMCP(t, session, "agenthail_create", args)
	if refused || out["taskId"] != nil {
		t.Fatalf("out=%v", out)
	}
	if _, found, err := r.TaskForChild(out["session"].(map[string]any)["id"].(string)); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
}

func TestATaskWhoseFirstTurnIsUnknownIsStillATask(t *testing.T) {
	r, fake, session, args := taskCreator(t)
	fake.startErr = surface.DeliveryOutcomeUnknown(errors.New("turn outcome unknown"))
	first, refused := callMCP(t, session, "agenthail_create", args)
	if refused || first["state"] != "running" || first["outcome"] != "unknown" || first["taskId"] == nil || first["sessionId"] == nil {
		t.Fatalf("first=%v", first)
	}
	again, refused := callMCP(t, session, "agenthail_create", args)
	if refused || again["taskId"] != first["taskId"] || again["sessionId"] != first["sessionId"] {
		t.Fatalf("again=%v first=%v", again, first)
	}
	if task, found, err := r.TaskForChild(first["sessionId"].(string)); err != nil || !found || task.State != registry.TaskRunning {
		t.Fatalf("task=%+v found=%v err=%v", task, found, err)
	}
}

func TestATaskWhoseStartFailsSettlesAsFailed(t *testing.T) {
	r, fake, session, args := taskCreator(t)
	fake.startErr = errors.New("the first turn was refused")
	out, refused := callMCP(t, session, "agenthail_create", args)
	if !refused {
		t.Fatalf("out=%v", out)
	}
	rows, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	failed := 0
	for _, row := range rows {
		if task, found, err := r.TaskForChild(row.ID); err == nil && found && task.State == registry.TaskFailed && strings.Contains(task.Result, "refused") {
			failed++
		}
	}
	if failed != 1 {
		t.Fatalf("failed tasks=%d", failed)
	}
}

func TestATaskCannotBeANotionThread(t *testing.T) {
	_, _, session, args := taskCreator(t)
	args["surface"] = "notion"
	if code := refusalCode(t, session, "agenthail_create", args); code != "invalid" {
		t.Fatalf("code=%q", code)
	}
}

func (f *taskFixture) stored(t *testing.T) registry.Task {
	t.Helper()
	task, found, err := f.r.TaskForChild(f.session.ID)
	if err != nil || !found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	return task
}

func (f *taskFixture) pass() {
	f.d.scanAndRelay(context.Background())
}

func (f *taskFixture) see(observation surface.TurnObservation) {
	f.surface.observations[f.session.ID] = &observation
	f.pass()
}

func (f *taskFixture) turn(id string, reply *surface.ReplyResult) {
	f.see(surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: id, Reply: reply})
}

func done(text string) *surface.ReplyResult {
	return &surface.ReplyResult{Text: text, Done: true}
}

func TestAReportSettlesOnceAndLaterWorkGoesToLatest(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "working", CompletedTurnID: "before"})
	report := func(id, status, text string) map[string]any {
		t.Helper()
		out, refused := callMCP(t, f.child, "agenthail_report", map[string]any{"status": status, "result": text, "clientRequestId": id})
		if refused {
			t.Fatalf("report: %v", out)
		}
		return out
	}
	if out := report("r1", "done", "first answer"); out["settled"] != true {
		t.Fatalf("out=%v", out)
	}
	if out := report("r1", "done", "first answer"); out["settled"] != true {
		t.Fatalf("a retry replays the first reply: %v", out)
	}
	if out := report("r2", "failed", "changed my mind"); out["settled"] != false {
		t.Fatalf("out=%v", out)
	}
	task := f.stored(t)
	if task.State != registry.TaskDone || task.Result != "first answer" || task.Kind != registry.TaskKindReported || task.Latest != "changed my mind" {
		t.Fatalf("task=%+v", task)
	}
	result := func() map[string]any {
		t.Helper()
		out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID})
		if refused {
			t.Fatalf("result: %v", out)
		}
		return out
	}
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "before", Reply: done("older than the report")}
	if out := result(); out["latest"] != "changed my mind" {
		t.Fatalf("a turn from before the report became latest: %v", out)
	}
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "later", Reply: done("kept working")}
	if out := result(); out["result"] != "first answer" || out["latest"] != "kept working" {
		t.Fatalf("out=%v", out)
	}
}

func TestOnlyAChildOfATaskCanReport(t *testing.T) {
	f := newTaskFixture(t)
	if code := refusalCode(t, f.parent, "agenthail_report", map[string]any{"status": "done", "result": "x", "clientRequestId": "p1"}); code != "not_a_task" {
		t.Fatalf("code=%q", code)
	}
	if code := refusalCode(t, f.child, "agenthail_report", map[string]any{"status": "maybe", "result": "x", "clientRequestId": "c1"}); code != "invalid" {
		t.Fatalf("code=%q", code)
	}
}

func TestArchivingCancelsTheTask(t *testing.T) {
	f := newTaskFixture(t)
	if response := runDashboardAction(t, f.d, `{"action":"session-archive","sessionId":"`+f.session.ID+`"}`); response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	if task := f.stored(t); task.State != registry.TaskCancelled {
		t.Fatalf("task=%+v", task)
	}
	f.turn("late", done("after the archive"))
	if _, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
		t.Fatal("the parent reads a cancelled task")
	}
	if task := f.stored(t); task.Latest != "" || task.State != registry.TaskCancelled {
		t.Fatalf("task=%+v", task)
	}
}

func TestAResultSurvivesARestart(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done("durable"))
	fresh := newTestDaemon(t, f.r, f.d.Surfaces)
	session := mcpSession(t, mcpServer(t, fresh), f.parentToken)
	out, refused := callMCP(t, session, "agenthail_result", map[string]any{"taskId": f.task.ID})
	if refused || out["result"] != "durable" || out["state"] != "done" || out["kind"] != "turn" {
		t.Fatalf("out=%v", out)
	}
}

func TestAResultIsClippedToFitAndNeverInvalid(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done(strings.Repeat("\"\n", 30<<10)))
	out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"target": f.session.ID})
	if refused || out["truncated"] != true {
		t.Fatalf("refused=%v truncated=%v", refused, out["truncated"])
	}
	encoded, _ := json.Marshal(out)
	if len(encoded) > mcpOutputLimit {
		t.Fatalf("encoded %d bytes", len(encoded))
	}
}

func TestOnlyTheParentOrAReaderOfTheChildSeesAResult(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done("private"))
	stranger := surface.Session{ID: "stranger", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Cwd: "/elsewhere"}
	if err := f.r.RegisterSession(stranger); err != nil {
		t.Fatal(err)
	}
	session := mcpSession(t, mcpServer(t, f.d), issueMCPToken(t, f.d, stranger))
	for _, id := range []int64{f.task.ID, f.task.ID + 1000} {
		out, refused := callMCP(t, session, "agenthail_result", map[string]any{"taskId": id})
		if !refused || out["code"] != "not_found" || out["message"] != errNoSuchTask.Message {
			t.Fatalf("a foreign task and a missing one must look alike: id=%d out=%v", id, out)
		}
	}
}

func TestReadingASettledResultClearsWake(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done("text"))
	if err := f.r.SetTaskWake(f.task.ID); err != nil {
		t.Fatal(err)
	}
	if _, refused := callMCP(t, f.child, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
		t.Fatal("a child can read its own task")
	}
	if task := f.stored(t); !task.Wake {
		t.Fatal("a reader other than the parent cleared wake")
	}
	if _, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
		t.Fatal("the parent reads its task")
	}
	if task := f.stored(t); task.Wake {
		t.Fatal("the parent read the result and wake stayed set")
	}
}

func TestIdleUnsettledTasksCountTowardTheChildrenLimit(t *testing.T) {
	f := newTaskFixture(t)
	running, err := f.r.RunningChildren(f.task.Parent)
	if err != nil || running != 1 {
		t.Fatalf("an idle child with an open task holds a slot: running=%d err=%v", running, err)
	}
	f.turn("t1", done("text"))
	if running, err = f.r.RunningChildren(f.task.Parent); err != nil || running != 0 {
		t.Fatalf("a settled task frees it: running=%d err=%v", running, err)
	}
	for index := range mcpChildrenLimit {
		child := surface.Session{ID: fmt.Sprintf("child-%d", index), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle}
		if err := f.r.RegisterSession(child); err != nil {
			t.Fatal(err)
		}
		if err := f.r.RecordSessionOrigin(registry.OriginOf(child.ID, f.task.Parent)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.r.CreateTask(f.task.Parent, child.ID, "work", time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	code := refusalCode(t, f.parent, "agenthail_create", map[string]any{"surface": "codex", "cwd": t.TempDir(), "message": "one more", "task": true, "clientRequestId": "over"})
	if code != "children_limit" {
		t.Fatalf("code=%q", code)
	}
}

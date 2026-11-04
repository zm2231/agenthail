package daemon

import (
	"errors"
	"os/exec"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func wantTask(t *testing.T, task registry.Task, state, kind, result, turn string) {
	t.Helper()
	if task.State != state || task.Kind != kind || task.Result != result || task.ResultTurnID != turn {
		t.Fatalf("want %s/%s %q on %q, got %+v", state, kind, result, turn, task)
	}
}

func wantSlots(t *testing.T, f *taskFixture, slots int) {
	t.Helper()
	if running, err := f.r.RunningChildren(f.task.Parent); err != nil || running != slots {
		t.Fatalf("slots=%d err=%v, want %d", running, err, slots)
	}
}

func catalogRow(t *testing.T, r *registry.Registry, session surface.Session, key string) {
	t.Helper()
	state := registry.CatalogSessionState{Session: session, Project: []byte(`{}`), Checkout: []byte(`{}`), ObservedAt: time.Now(), ProjectionFingerprint: `{"id":"` + key + `"}`}
	event := registry.CatalogEvent{DedupeKey: "row:" + key, Type: "session.upserted", EntityID: session.ID, Payload: []byte(`{}`)}
	if _, _, err := r.RecordCatalogSession(state, event); err != nil {
		t.Fatal(err)
	}
}

func dropFromCatalog(t *testing.T, f *taskFixture, gone map[string]struct{}) {
	t.Helper()
	catalogRow(t, f.r, f.session, "child")
	for range 2 {
		if _, err := f.r.RecordEndedCatalogDiscovery(f.session.Surface, map[string]struct{}{}, time.Now(), 2, gone); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAPlainTaskChildIsObservedAndSettlesOnItsOwn(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1"})
	if f.surface.observeCalls.Load() == 0 {
		t.Fatal("a task child with no route or queue row was not observed")
	}
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("task=%+v", task)
	}
	f.turn("t1", done("the final text"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "the final text", "t1")
	wantSlots(t, f, 0)
	f.turn("t2", done("a later turn"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "the final text", "t1")
	calls := f.surface.observeCalls.Load()
	f.pass()
	if f.surface.observeCalls.Load() != calls {
		t.Fatal("a settled child is still watched")
	}
}

func TestAFirstObservationAlreadyIdleSettles(t *testing.T) {
	f := newTaskFixture(t)
	if _, found, err := f.r.RuntimeState(f.session.ID); err != nil || found {
		t.Fatalf("found=%v err=%v", found, err)
	}
	f.turn("first", done("finished before we looked"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "finished before we looked", "first")
}

func TestATurnThatEndedBeforeTheTaskRowExistedSettles(t *testing.T) {
	f := newTaskFixture(t)
	early := surface.Session{ID: "early", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
	if err := f.r.RegisterSession(early); err != nil {
		t.Fatal(err)
	}
	observation := surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "early-turn", Reply: done("already over")}
	if err := f.r.SaveRuntimeState(early.ID, observation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.CreateTask(f.task.Parent, early.ID, "work", time.Now()); err != nil {
		t.Fatal(err)
	}
	f.surface.observations[early.ID] = &observation
	f.pass()
	task, _, err := f.r.TaskForChild(early.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTask(t, task, registry.TaskDone, registry.TaskKindTurn, "already over", "early-turn")
}

func exitedPID(t *testing.T) int {
	t.Helper()
	command := exec.Command("true")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	return command.ProcessState.Pid()
}

func claudeTaskChild(t *testing.T, f *taskFixture, pid int) (surface.Session, *daemonSurface) {
	t.Helper()
	child := surface.Session{ID: "claude-child", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusIdle, PID: pid}
	claude := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{child.ID: child}, observations: map[string]*surface.TurnObservation{}}
	f.d.Surfaces = append(f.d.Surfaces, claude)
	if err := f.r.RegisterSession(child); err != nil {
		t.Fatal(err)
	}
	if err := f.r.RecordSessionOrigin(registry.OriginOf(child.ID, f.task.Parent)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.CreateTask(f.task.Parent, child.ID, "work", time.Now()); err != nil {
		t.Fatal(err)
	}
	return child, claude
}

func TestAnExitedClaudeChildSettlesFromItsTranscript(t *testing.T) {
	f := newTaskFixture(t)
	child, claude := claudeTaskChild(t, f, exitedPID(t))
	claude.observations[child.ID] = &surface.TurnObservation{Status: surface.StatusOffline, CompletedTurnID: "m1", Reply: done("written before it exited")}
	f.pass()
	task, _, err := f.r.TaskForChild(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTask(t, task, registry.TaskDone, registry.TaskKindTurn, "written before it exited", "m1")
}

func TestAnInterruptedClaudeTurnSettlesFailed(t *testing.T) {
	f := newTaskFixture(t)
	child, claude := claudeTaskChild(t, f, 0)
	claude.observations[child.ID] = &surface.TurnObservation{Status: surface.StatusIdle, TerminalTurnID: "u1", TerminalError: "turn interrupted"}
	f.pass()
	task, _, err := f.r.TaskForChild(child.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantTask(t, task, registry.TaskFailed, registry.TaskKindTurn, "turn interrupted", "u1")
}

func TestATurnWithNoTextSettlesDoneAndEmpty(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusIdle, TerminalTurnID: "silent"})
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "", "silent")
}

func TestATurnErrorSettlesFailed(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", &surface.ReplyResult{Done: true, Error: "model refused"})
	wantTask(t, f.stored(t), registry.TaskFailed, registry.TaskKindTurn, "model refused", "t1")
}

func TestAFollowUpSentBeforeSettlingGoesToLatest(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done("the first turn"))
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t2", Reply: done("the follow-up")}
	out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID})
	if refused || out["result"] != "the first turn" || out["latest"] != "the follow-up" {
		t.Fatalf("out=%v", out)
	}
}

func TestBackgroundWorkKeepsATaskOpenUntilItStops(t *testing.T) {
	f := newTaskFixture(t)
	running := &surface.BackgroundTasks{Running: true, Since: time.Now(), Source: surface.BackgroundSourceTranscript}
	f.see(surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "held", Reply: done("foreground done"), BackgroundTasks: running})
	f.pass()
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("settled with background work running: %+v", task)
	}
	f.turn("held", done("foreground done"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "foreground done", "held")
}

func TestABusyStatusWithNoActiveTurnSettlesNothing(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, CompletedTurnID: "t1", Reply: done("older")})
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("task=%+v", task)
	}
}

func TestAnActiveGoalKeepsTheTaskOpenUntilALaterTurnCompletesIt(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goal = &surface.GoalState{Status: surface.GoalStatusActive, CreatedAt: time.Now()}
	f.turn("t1", done("first idle turn"))
	f.surface.goal.Status = surface.GoalStatusBlocked
	f.turn("t2", done("stuck"))
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("a goal that hasn't completed settled the task: %+v", task)
	}
	f.surface.goal.Status = surface.GoalStatusComplete
	f.turn("t3", done("goal met"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindGoal, "goal met", "t3")
}

func TestAnUnreadableGoalWaitsAndIsReadAgain(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goalErr = errors.New("app server down")
	f.turn("t1", done("text"))
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("task=%+v", task)
	}
	f.surface.goalErr = nil
	f.pass()
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatal("the goal was read again inside its pace")
	}
	delete(f.d.taskGoals.reads, f.session.ID)
	f.pass()
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "text", "t1")
}

func TestAGoalClearedBetweenTurnsSettlesOnTheNextRead(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goal = &surface.GoalState{Status: surface.GoalStatusActive, CreatedAt: time.Now()}
	f.turn("t1", done("text"))
	f.surface.goal = nil
	if !f.d.taskGoals.due(f.session.ID, "t1", time.Now().Add(taskGoalRecheck)) {
		t.Fatal("the goal read is not due after its pace")
	}
	delete(f.d.taskGoals.reads, f.session.ID)
	f.pass()
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "text", "t1")
}

func TestGoalPacingEndsWithTheTaskHoweverItEnds(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goal = &surface.GoalState{Status: surface.GoalStatusActive, CreatedAt: time.Now()}
	f.turn("t1", done("text"))
	if _, found := f.d.taskGoals.reads[f.session.ID]; !found {
		t.Fatal("a goal read leaves its pacing entry")
	}
	if response := runDashboardAction(t, f.d, `{"action":"session-archive","sessionId":"`+f.session.ID+`"}`); response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	f.pass()
	if _, found := f.d.taskGoals.reads[f.session.ID]; found {
		t.Fatal("the pacing entry outlived the cancelled task")
	}
}

func TestACompleteGoalWaitsForTheTurnThatCompletedIt(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goal = &surface.GoalState{Status: surface.GoalStatusComplete, CreatedAt: time.Now()}
	f.surface.onGoal = func() {
		f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t2", CompletedTurnID: "t1", Reply: done("the turn before")}
	}
	f.turn("t1", done("the turn before"))
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("settled on the turn before the one that completed the goal: %+v", task)
	}
	f.surface.onGoal = nil
	f.turn("t2", done("the goal's own turn"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindGoal, "the goal's own turn", "t2")
}

func TestAGoneChildSettlesWithTheTurnItsGoalHeldOpen(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.goal = &surface.GoalState{Status: surface.GoalStatusActive, CreatedAt: time.Now()}
	f.turn("t1", done("the last thing it said"))
	dropFromCatalog(t, f, map[string]struct{}{f.session.ID: {}})
	f.surface.observeErr = errors.New("thread not found")
	f.pass()
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "the last thing it said", "t1")
	wantSlots(t, f, 0)
}

func TestAGoneChildWithNoTurnSettlesFailed(t *testing.T) {
	f := newTaskFixture(t)
	dropFromCatalog(t, f, map[string]struct{}{f.session.ID: {}})
	f.surface.observeErr = errors.New("thread not found")
	f.pass()
	wantTask(t, f.stored(t), registry.TaskFailed, registry.TaskKindTurn, taskChildEndedNote, "")
	f.pass()
	wantTask(t, f.stored(t), registry.TaskFailed, registry.TaskKindTurn, taskChildEndedNote, "")
}

type resumableTaskSurface struct{ *taskSurface }

func (resumableTaskSurface) EndedConversation(string) (bool, time.Time) {
	return true, time.Now()
}

func TestAnExitedChildThatCanResumeStaysOpenAndHoldsNoSlot(t *testing.T) {
	t.Run("a kept row", func(t *testing.T) {
		f := newTaskFixture(t)
		dropFromCatalog(t, f, nil)
		f.see(surface.TurnObservation{Status: surface.StatusOffline})
		if task := f.stored(t); task.State != registry.TaskRunning {
			t.Fatalf("task=%+v", task)
		}
		wantSlots(t, f, 0)
	})
	t.Run("no row, but the surface can resume it", func(t *testing.T) {
		f := newTaskFixture(t)
		f.d.Surfaces = []surface.Surface{resumableTaskSurface{f.surface}}
		dropFromCatalog(t, f, map[string]struct{}{f.session.ID: {}})
		f.see(surface.TurnObservation{Status: surface.StatusOffline})
		if task := f.stored(t); task.State != registry.TaskRunning {
			t.Fatalf("task=%+v", task)
		}
		wantSlots(t, f, 0)
	})
}

func TestAChildWhoseOwnerDiedStaysOpenAndHoldsNoSlot(t *testing.T) {
	f := newTaskFixture(t)
	wantSlots(t, f, 1)
	f.see(surface.TurnObservation{Status: surface.StatusIdle, TurnUnconfirmed: true})
	if task := f.stored(t); task.State != registry.TaskRunning || !task.Stalled {
		t.Fatalf("task=%+v", task)
	}
	wantSlots(t, f, 0)
	f.session.Status = surface.StatusBusy
	if err := f.r.RegisterSession(f.session); err != nil {
		t.Fatal(err)
	}
	wantSlots(t, f, 0)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1"})
	wantSlots(t, f, 1)
	f.turn("t1", done("the owner came back"))
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "the owner came back", "t1")
}

func TestAnUnconfirmedTurnSettlesNothingEvenWithAnOlderCompletion(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("older"), TurnUnconfirmed: true})
	if task := f.stored(t); task.State != registry.TaskRunning {
		t.Fatalf("task=%+v", task)
	}
}

func TestAFailingObserveDelaysATaskButDoesNotStarveIt(t *testing.T) {
	f := newTaskFixture(t)
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("text")}
	f.surface.observeErr = errors.New("app server down")
	f.pass()
	calls := f.surface.observeCalls.Load()
	f.surface.observeErr = nil
	f.pass()
	if f.surface.observeCalls.Load() != calls {
		t.Fatal("the failure backoff did not apply")
	}
	f.d.retryMu.Lock()
	retry := f.d.observeRetry[f.session.ID]
	if wait := time.Until(retry.retryAt); wait > time.Minute {
		t.Errorf("backoff of %s is past its cap", wait)
	}
	retry.retryAt = time.Now()
	f.d.observeRetry[f.session.ID] = retry
	f.d.retryMu.Unlock()
	f.pass()
	wantTask(t, f.stored(t), registry.TaskDone, registry.TaskKindTurn, "text", "t1")
}

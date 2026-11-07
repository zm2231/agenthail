package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/testkit"
)

// Well under these tests' 20 s timeout, whose final read would also find the event.
const waitWokenPromptly = 10 * time.Second

func (f *taskFixture) wait(t *testing.T, args map[string]any) map[string]any {
	t.Helper()
	if args["tasks"] == nil {
		args["tasks"] = []string{fmt.Sprint(f.task.ID)}
	}
	out, refused := callMCP(t, f.parent, "agenthail_wait", args)
	if refused {
		t.Fatalf("wait refused: %v", out)
	}
	return out
}

func readyOf(t *testing.T, out map[string]any, kind string) map[string]any {
	t.Helper()
	ready, _ := out["ready"].([]any)
	if len(ready) != 1 {
		t.Fatalf("ready=%v", out["ready"])
	}
	item := ready[0].(map[string]any)
	if item["type"] != kind {
		t.Fatalf("want %s, got %v", kind, item)
	}
	return item
}

func (f *taskFixture) queuedFor(t *testing.T, session, key string) int {
	t.Helper()
	var count int
	if err := f.r.WriteDB().QueryRow(`SELECT COUNT(*) FROM message_queue WHERE session_id=? AND delivery_key=? AND status<>'canceled'`, session, key).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

func (f *taskFixture) subscribers() int {
	f.d.events.mu.Lock()
	defer f.d.events.mu.Unlock()
	return len(f.d.events.subscribers)
}

// awaitSubscribers reports whether at least n event subscribers are open
// before stop closes.
func (f *taskFixture) awaitSubscribers(n int, stop <-chan struct{}) bool {
	for f.subscribers() < n {
		select {
		case <-stop:
			return false
		case <-time.After(time.Millisecond):
		}
	}
	return true
}

func (f *taskFixture) dropSubscribers() {
	f.d.events.mu.Lock()
	defer f.d.events.mu.Unlock()
	for id, subscriber := range f.d.events.subscribers {
		delete(f.d.events.subscribers, id)
		close(subscriber)
	}
}

type waitArrival struct {
	name string
	make func(f *taskFixture)
	kind string
}

func waitArrivals(t *testing.T) []waitArrival {
	t.Helper()
	return []waitArrival{
		{"settle", func(f *taskFixture) {
			f.turn("t1", done("the result"))
		}, "settled"},
		{"permission prompt", func(f *taskFixture) {
			f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1", WaitingOn: &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1"}})
		}, "request"},
		{"stopped goal", func(f *taskFixture) {
			f.surface.setGoal(&surface.GoalState{Status: surface.GoalStatusBlocked, CreatedAt: time.Now(), UpdatedAt: time.Now()})
			f.turn("t1", done("stuck"))
		}, "goal_stopped"},
	}
}

func TestAWaitReturnsWithoutEventsWhenSomethingIsAlreadyReady(t *testing.T) {
	for _, arrival := range waitArrivals(t) {
		t.Run(arrival.name, func(t *testing.T) {
			f := newTaskFixture(t)
			arrival.make(f)
			readyOf(t, f.wait(t, map[string]any{"timeoutSec": 5}), arrival.kind)
		})
	}
}

func TestAnEventBetweenTheReadAndTheSubscribeWakesTheWait(t *testing.T) {
	for _, arrival := range waitArrivals(t) {
		t.Run(arrival.name, func(t *testing.T) {
			f := newTaskFixture(t)
			once := false
			f.d.taskWaitRead = func() {
				if !once {
					once = true
					arrival.make(f)
				}
			}
			started := time.Now()
			readyOf(t, f.wait(t, map[string]any{"timeoutSec": 20}), arrival.kind)
			if elapsed := time.Since(started); elapsed >= waitWokenPromptly {
				t.Fatalf("the event was missed and only the timeout found it: %s", elapsed)
			}
		})
	}
}

func TestADroppedSubscriptionDoesNotLoseTheEvent(t *testing.T) {
	for _, arrival := range waitArrivals(t) {
		t.Run(arrival.name, func(t *testing.T) {
			f := newTaskFixture(t)
			reads := 0
			returned := make(chan struct{})
			dropped := make(chan struct{})
			f.d.taskWaitRead = func() {
				reads++
				if reads == 1 {
					before := f.subscribers()
					go func() {
						defer close(dropped)
						if !f.awaitSubscribers(before+1, returned) {
							return
						}
						f.dropSubscribers()
						arrival.make(f)
						f.dropSubscribers()
					}()
				}
			}
			started := time.Now()
			out := f.wait(t, map[string]any{"timeoutSec": 20})
			close(returned)
			<-dropped
			readyOf(t, out, arrival.kind)
			if reads < 2 {
				t.Fatalf("reads=%d", reads)
			}
			if elapsed := time.Since(started); elapsed >= waitWokenPromptly {
				t.Fatalf("took %s", elapsed)
			}
		})
	}
}

func (f *taskFixture) noise(t *testing.T) {
	t.Helper()
	if _, err := f.d.events.publish("test.noise", "noise", nil); err != nil {
		t.Fatal(err)
	}
}

func (f *taskFixture) forceHubReset(t *testing.T, after uint64) {
	t.Helper()
	f.noise(t)
	f.noise(t)
	f.d.events.mu.Lock()
	f.d.events.history = f.d.events.history[len(f.d.events.history)-1:]
	f.d.events.mu.Unlock()
	_, _, reset, cancel := f.d.events.subscribe(after)
	cancel()
	if !reset {
		t.Fatalf("a subscribe from %d did not reset", after)
	}
}

func TestAHubResetDoesNotLoseTheEvent(t *testing.T) {
	for _, arrival := range waitArrivals(t) {
		t.Run(arrival.name, func(t *testing.T) {
			f := newTaskFixture(t)
			f.noise(t)
			reads := 0
			f.d.taskWaitRead = func() {
				reads++
				if reads == 1 {
					after := f.d.events.cursor()
					arrival.make(f)
					f.forceHubReset(t, after)
				}
			}
			started := time.Now()
			readyOf(t, f.wait(t, map[string]any{"timeoutSec": 20}), arrival.kind)
			if reads < 2 {
				t.Fatalf("reads=%d", reads)
			}
			if elapsed := time.Since(started); elapsed >= waitWokenPromptly {
				t.Fatalf("took %s", elapsed)
			}
		})
	}
}

func TestAGoalThatStopsAgainIsReportedAgain(t *testing.T) {
	f := newTaskFixture(t)
	created := time.Now()
	stop := func(at time.Time) {
		f.surface.setGoal(&surface.GoalState{Status: surface.GoalStatusBlocked, CreatedAt: created, UpdatedAt: at})
	}
	stop(created)
	first := f.wait(t, map[string]any{"timeoutSec": 0})
	readyOf(t, first, "goal_stopped")
	again := f.wait(t, map[string]any{"timeoutSec": 0, "cursor": first["cursor"]})
	if ready := again["ready"].([]any); len(ready) != 0 {
		t.Fatalf("the same stop repeated: %v", again)
	}
	stop(created.Add(time.Minute))
	readyOf(t, f.wait(t, map[string]any{"timeoutSec": 0, "cursor": again["cursor"]}), "goal_stopped")
}

func TestAWaitCursorDoesNotRepeatWhatItReported(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1", WaitingOn: &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1"}})
	first := f.wait(t, map[string]any{"timeoutSec": 0})
	readyOf(t, first, "request")
	again := f.wait(t, map[string]any{"timeoutSec": 0, "cursor": first["cursor"]})
	if ready := again["ready"].([]any); len(ready) != 0 || len(again["running"].([]any)) != 1 {
		t.Fatalf("again=%v", again)
	}
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1", WaitingOn: &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-2"}})
	readyOf(t, f.wait(t, map[string]any{"timeoutSec": 0, "cursor": again["cursor"]}), "request")
}

func TestAWaitOnlyEndsEarlyWhenNothingIsLeftRunning(t *testing.T) {
	f := newTaskFixture(t)
	f.turn("t1", done("over"))
	first := f.wait(t, map[string]any{"timeoutSec": 0})
	started := time.Now()
	again := f.wait(t, map[string]any{"timeoutSec": 20, "cursor": first["cursor"]})
	if ready := again["ready"].([]any); len(ready) != 0 || time.Since(started) > 5*time.Second {
		t.Fatalf("waited %s for nothing: %v", time.Since(started), again)
	}
}

func TestATimedOutWaitLeavesTheChildRunningAndOneNoticeFollows(t *testing.T) {
	f := newTaskFixture(t)
	out := f.wait(t, map[string]any{"timeoutSec": 1})
	if ready := out["ready"].([]any); len(ready) != 0 || len(out["running"].([]any)) != 1 || out["next"] == nil {
		t.Fatalf("out=%v", out)
	}
	if task := f.stored(t); task.State != registry.TaskRunning || !task.Wake {
		t.Fatalf("task=%+v", task)
	}
	f.turn("t1", done("over"))
	f.pass()
	f.pass()
	queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID))
	if queued != 1 {
		t.Fatalf("notices=%d", queued)
	}
	if task := f.stored(t); task.Wake {
		t.Fatalf("task=%+v", task)
	}
}

func TestAPollDoesNotAskForANotice(t *testing.T) {
	f := newTaskFixture(t)
	f.wait(t, map[string]any{"timeoutSec": 0})
	if task := f.stored(t); task.Wake {
		t.Fatal("a poll armed a notice")
	}
}

func TestReadingTheResultFirstMeansNoNotice(t *testing.T) {
	for name, read := range map[string]func(t *testing.T, f *taskFixture){
		"agenthail_result": func(t *testing.T, f *taskFixture) {
			if out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
				t.Fatalf("out=%v", out)
			}
		},
		"a wait": func(t *testing.T, f *taskFixture) {
			readyOf(t, f.wait(t, map[string]any{"timeoutSec": 0}), "settled")
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newTaskFixture(t)
			f.wait(t, map[string]any{"timeoutSec": 1})
			f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("over")}
			f.d.settleTaskFromObservation(context.Background(), f.surface, &f.session, f.surface.observations[f.session.ID])
			read(t, f)
			f.pass()
			if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 0 {
				t.Fatalf("notices=%d", queued)
			}
		})
	}
	t.Run("a notice not yet delivered is cancelled", func(t *testing.T) {
		f := newTaskFixture(t)
		f.wait(t, map[string]any{"timeoutSec": 1})
		f.turn("t1", done("over"))
		f.pass()
		if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 1 {
			t.Fatalf("notices=%d", queued)
		}
		if out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
			t.Fatalf("out=%v", out)
		}
		if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 0 {
			t.Fatalf("notices=%d", queued)
		}
	})
}

func TestTheNoticeCrossesARestartExactlyOnce(t *testing.T) {
	f := newTaskFixture(t)
	f.wait(t, map[string]any{"timeoutSec": 1})
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("over")}
	f.d.settleTaskFromObservation(context.Background(), f.surface, &f.session, f.surface.observations[f.session.ID])
	if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 0 {
		t.Fatalf("notices before the pass=%d", queued)
	}
	fresh := newTestDaemon(t, f.r, f.d.Surfaces)
	fresh.scanAndRelay(context.Background())
	fresh.scanAndRelay(context.Background())
	if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 1 {
		t.Fatalf("notices=%d", queued)
	}
	if err := f.r.ArmTaskWake(f.task.ID); err != nil {
		t.Fatal(err)
	}
	if task := f.stored(t); task.Wake {
		t.Fatal("a settled task took a wake")
	}
	if _, err := f.r.WriteDB().Exec(`UPDATE tasks SET wake=1 WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	newTestDaemon(t, f.r, f.d.Surfaces).scanAndRelay(context.Background())
	if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 1 {
		t.Fatalf("a crash before the wake cleared repeated the notice: %d", queued)
	}
}

func TestArchivingClearsAWake(t *testing.T) {
	f := newTaskFixture(t)
	f.wait(t, map[string]any{"timeoutSec": 1})
	if response := runDashboardAction(t, f.d, `{"action":"session-archive","sessionId":"`+f.session.ID+`"}`); response.Code != 200 {
		t.Fatalf("status=%d", response.Code)
	}
	f.pass()
	if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 0 {
		t.Fatalf("notices=%d", queued)
	}
}

func TestAWaitReturningARequestWithdrawsTheWaitersOwnNotice(t *testing.T) {
	f := newTaskFixture(t)
	f.see(surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "t1", WaitingOn: &surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1"}})
	key := waitingNoticeKey(f.session.ID, "prompt-1")
	testkit.Eventually(t, "the waiting notice", 5*time.Second, func() bool { return f.queuedFor(t, f.task.Parent, key) == 1 })
	readyOf(t, f.wait(t, map[string]any{"timeoutSec": 0}), "request")
	if queued := f.queuedFor(t, f.task.Parent, key); queued != 0 {
		t.Fatalf("notice after the wait=%d", queued)
	}
}

func TestANoticeStillOnItsWayIsDroppedOnceTheWaiterReadTheRequest(t *testing.T) {
	f := newTaskFixture(t)
	waiting := surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1"}
	to, ok := f.d.noticeRecipient(f.session)
	if !ok || to.sessionID != f.task.Parent {
		t.Fatalf("to=%+v ok=%v", to, ok)
	}
	caller := &mcpCaller{session: surface.Session{ID: f.task.Parent}}
	f.d.noteWaitItemRead(caller, mcpWaitItem{Type: "request", Child: f.session.ID, waiting: &waiting})
	f.d.noticeWaiting(f.session, waiting, surface.Request{}, false)
	if queued := f.queuedFor(t, f.task.Parent, waitingNoticeKey(f.session.ID, waiting.ID)); queued != 0 {
		t.Fatalf("notices=%d", queued)
	}
}

func TestARequestReadIsForgottenOnceTheTaskSettles(t *testing.T) {
	f := newTaskFixture(t)
	f.d.noteWaitReadRequest(f.session.ID, f.task.Parent, surface.Waiting{Kind: surface.WaitingApproval, ID: "prompt-1"})
	f.pass()
	if len(f.d.taskRequestsRead) != 1 {
		t.Fatalf("forgotten while running: %v", f.d.taskRequestsRead)
	}
	f.turn("t1", done("over"))
	f.pass()
	if len(f.d.taskRequestsRead) != 0 {
		t.Fatalf("kept after settling: %v", f.d.taskRequestsRead)
	}
}

func TestAWakeStillOnItsWayIsDroppedOnceTheParentReadTheResult(t *testing.T) {
	f := newTaskFixture(t)
	f.wait(t, map[string]any{"timeoutSec": 1})
	f.surface.observations[f.session.ID] = &surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "t1", Reply: done("over")}
	f.d.settleTaskFromObservation(context.Background(), f.surface, &f.session, f.surface.observations[f.session.ID])
	inFlight := f.stored(t)
	if !inFlight.Wake {
		t.Fatalf("task=%+v", inFlight)
	}
	if out, refused := callMCP(t, f.parent, "agenthail_result", map[string]any{"taskId": f.task.ID}); refused {
		t.Fatalf("out=%v", out)
	}
	if err := f.d.wakeTaskParent(inFlight); err != nil {
		t.Fatal(err)
	}
	if queued := f.queuedFor(t, f.task.Parent, taskWakeKey(f.task.ID)); queued != 0 {
		t.Fatalf("notices=%d", queued)
	}
}

func TestAWaitOfAReaderDoesNotArmOrClearTheParentsWake(t *testing.T) {
	f := newTaskFixture(t)
	if out, refused := callMCP(t, f.child, "agenthail_wait", map[string]any{"tasks": []string{fmt.Sprint(f.task.ID)}, "timeoutSec": 1}); refused {
		t.Fatalf("out=%v", out)
	}
	if task := f.stored(t); task.Wake {
		t.Fatal("a reader armed the parent's wake")
	}
}

func TestAWaitRefusesWhatItCannotRead(t *testing.T) {
	f := newTaskFixture(t)
	for name, test := range map[string]struct {
		args map[string]any
		code string
	}{
		"no tasks":      {map[string]any{"tasks": []string{}}, "invalid"},
		"missing task":  {map[string]any{"tasks": []string{"99999"}}, "not_found"},
		"negative":      {map[string]any{"tasks": []string{"1"}, "timeoutSec": -1}, "invalid"},
		"a bad cursor":  {map[string]any{"tasks": []string{"1"}, "cursor": "!!!"}, "invalid"},
		"a huge cursor": {map[string]any{"tasks": []string{"1"}, "cursor": strings.Repeat("W", mcpOutputLimit+1)}, "invalid"},
		"too many":      {map[string]any{"tasks": strings.Split(strings.Repeat("1,", mcpWaitMaxTasks+1), ",")}, "invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			if code := refusalCode(t, f.parent, "agenthail_wait", test.args); code != test.code {
				t.Fatalf("code=%q", code)
			}
		})
	}
}

func TestAWaitOnManyLargeResultsFitsAndReturnsTheRestNext(t *testing.T) {
	f := newTaskFixture(t)
	ids := []string{}
	for index := range mcpWaitMaxTasks {
		child := surface.Session{ID: fmt.Sprintf("big-%d", index), Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Status: surface.StatusIdle, Source: "vscode", Transport: "desktop"}
		if err := f.r.RegisterSession(child); err != nil {
			t.Fatal(err)
		}
		task, err := f.r.CreateTask(f.task.Parent, child.ID, "work", time.Now())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.r.SettleTask(child.ID, registry.TaskSettlement{State: registry.TaskDone, Kind: registry.TaskKindTurn, Result: strings.Repeat("\"\n", 8<<10)}, time.Now()); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, fmt.Sprint(task.ID))
	}
	cursor := ""
	seen := 0
	for range mcpWaitMaxTasks + 1 {
		out := f.wait(t, map[string]any{"tasks": ids, "timeoutSec": 0, "cursor": cursor})
		encoded, _ := json.Marshal(out)
		if len(encoded) > mcpOutputLimit {
			t.Fatalf("encoded %d bytes", len(encoded))
		}
		seen += len(out["ready"].([]any))
		cursor, _ = out["cursor"].(string)
		if seen == len(ids) {
			return
		}
		if len(out["ready"].([]any)) == 0 {
			t.Fatalf("stalled at %d of %d", seen, len(ids))
		}
	}
	t.Fatalf("read %d of %d", seen, len(ids))
}

func TestAnUnhostedClaudeWaitIsCappedBelowClaudesOwnLimit(t *testing.T) {
	for _, test := range []struct {
		name    string
		session surface.Session
		want    int
	}{
		{"terminal Claude", surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI}, mcpWaitUnhostedClaudeSec},
		{"hosted Claude", surface.Session{Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeHost}, mcpWaitMaxSec},
		{"Codex", surface.Session{Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexAppServer}, mcpWaitMaxSec},
	} {
		if got := mcpWaitLimitSec(test.session); got != test.want {
			t.Errorf("%s: %d", test.name, got)
		}
	}
	if mcpWaitUnhostedClaudeSec >= 60 {
		t.Fatal("the cap has to return before Claude's measured 60 s")
	}
}

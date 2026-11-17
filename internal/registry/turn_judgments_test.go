package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestATurnEndCountsTheAsksChildrenAndRelaysThatWillWakeTheSession(t *testing.T) {
	r := coordinatorRegistry(t, "lead", "idle-child", "other")
	if err := r.RecordObservedSession(surface.Session{ID: "busy-child", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy, Cwd: "/work"}); err != nil {
		t.Fatal(err)
	}
	facts, err := r.TurnEndFacts("lead")
	if err != nil || facts.Pending() {
		t.Fatalf("a session with nothing outstanding: %+v %v", facts, err)
	}

	for _, child := range []string{"busy-child", "idle-child"} {
		if err := r.RecordSessionOrigin(OriginOf(child, "lead")); err != nil {
			t.Fatal(err)
		}
	}
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := r.RaiseAsk(Ask{SessionID: "lead", Origin: AskOriginAgent, Title: "Ship?", Questions: []AskQuestion{{Question: "Ship?", Options: []AskOption{{ID: "o0", Label: "Yes"}}}}, RoutedTo: AskToPerson}, at); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRouteWithOptions("other", "lead", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRouteWithOptions("other", "lead", "", false); err != nil {
		t.Fatal(err)
	}
	facts, err = r.TurnEndFacts("lead")
	if err != nil {
		t.Fatal(err)
	}
	if facts.OpenAsks != 1 || facts.ChildrenWorking != 1 || facts.AwaitedRelays != 1 || !facts.Pending() {
		t.Fatalf("facts = %+v, want one open ask, one working child and one awaited reply", facts)
	}
}

func TestATurnJudgmentIsKeptPerTurnAndOldOnesAreDropped(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	record := func(turn string, ended time.Time, promise float64) {
		t.Helper()
		if err := r.RecordTurnJudgment(TurnJudgment{SessionID: "worker", TurnID: turn, EndedAt: ended, Model: "d1-3b", PromiseModel: "d1-3b-promise", Promise: promise, StateP: map[string]float64{"done": 0.9}, Facts: TurnFacts{BackgroundRunning: true, BackgroundSource: "host", BackgroundTasks: 1}}); err != nil {
			t.Fatal(err)
		}
	}
	record("old", at.Add(-31*24*time.Hour), 0.1)
	record("t1", at, 0.2)
	record("t1", at, 0.7)
	record("t2", at.Add(time.Minute), 0.3)

	got, err := r.TurnJudgments("worker", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].TurnID != "t2" || got[1].TurnID != "t1" || got[1].Promise != 0.7 {
		t.Fatalf("judgments = %+v, want t2 then t1 with its latest promise, and the 31-day-old one gone", got)
	}
	if got[1].StateP["done"] != 0.9 || !got[1].Facts.BackgroundRunning || got[1].Facts.BackgroundSource != "host" || got[1].PromiseModel != "d1-3b-promise" || !got[1].JudgedAt.IsZero() {
		t.Fatalf("t1 read back as %+v", got[1])
	}
}

func TestATurnPushIsClaimedOnceAndOnlyWhileTheTurnAndItsAskStillStand(t *testing.T) {
	r := coordinatorRegistry(t, "worker")
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, turn := range []string{"t1", "t2"} {
		if err := r.RecordTurnJudgment(TurnJudgment{SessionID: "worker", TurnID: turn, EndedAt: at, State: "waiting_person"}); err != nil {
			t.Fatal(err)
		}
	}
	runtime := func(active, completed string) {
		t.Helper()
		if err := r.SaveRuntimeState("worker", surface.TurnObservation{Status: surface.StatusIdle, ActiveTurnID: active, CompletedTurnID: completed}); err != nil {
			t.Fatal(err)
		}
	}
	claim := func(turn, ask string) bool {
		t.Helper()
		claimed, err := r.ClaimTurnPush("worker", turn, ask, at)
		if err != nil {
			t.Fatal(err)
		}
		return claimed
	}

	runtime("t2", "t1")
	if claim("t1", "") {
		t.Fatal("a turn was pushed while the next one ran")
	}
	runtime("", "t2")
	if claim("t1", "") {
		t.Fatal("a turn was pushed after the next one completed")
	}
	raised, err := r.RaiseAsk(Ask{SessionID: "worker", Origin: AskOriginAgent, Title: "Ship?", Questions: []AskQuestion{{Question: "Ship?", Options: []AskOption{{ID: "o0", Label: "Yes"}}}}, RoutedTo: AskToPerson}, at)
	if err != nil {
		t.Fatal(err)
	}
	if claim("t2", "") {
		t.Fatal("a turn was pushed after an ask came to its row")
	}
	pushedAt := func() time.Time {
		t.Helper()
		judgments, err := r.TurnJudgments("worker", 10)
		if err != nil {
			t.Fatal(err)
		}
		for _, j := range judgments {
			if j.TurnID == "t2" {
				return j.PushedAt
			}
		}
		t.Fatal("t2 has no judgment")
		return time.Time{}
	}
	if !claim("t2", raised.ID) || claim("t2", raised.ID) {
		t.Fatal("the turn's push was not claimed exactly once")
	}
	if !pushedAt().IsZero() {
		t.Fatal("a claimed push reads as pushed before it is confirmed")
	}
	if err := r.ReleaseTurnPush("worker", "t2", at); err != nil {
		t.Fatal(err)
	}
	if !claim("t2", raised.ID) {
		t.Fatal("a released push could not be claimed again")
	}
	if confirmed, err := r.ConfirmTurnPush("worker", "t2", at); err != nil || !confirmed || !pushedAt().Equal(at) {
		t.Fatalf("confirmed=%v err=%v pushedAt=%v", confirmed, err, pushedAt())
	}
	if err := r.ReleaseTurnPush("worker", "t2", at); err != nil || !pushedAt().Equal(at) {
		t.Fatal("a confirmed push was released")
	}
	if err := r.RecordTurnJudgment(TurnJudgment{SessionID: "worker", TurnID: "t2", EndedAt: at, State: "waiting_person", Promise: 0.4}); err != nil {
		t.Fatal(err)
	}
	if claim("t2", raised.ID) {
		t.Fatal("judging the turn again let its push be claimed again")
	}
}

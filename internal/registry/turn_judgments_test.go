package registry

import (
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestATurnEndCountsTheAsksChildrenAndRelaysThatWillWakeTheSession(t *testing.T) {
	r := coordinatorRegistry(t, "lead", "idle-child", "other")
	if err := r.RegisterSession(surface.Session{ID: "busy-child", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Status: surface.StatusBusy, Cwd: "/work"}); err != nil {
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
		if err := r.RecordTurnJudgment(TurnJudgment{SessionID: "worker", TurnID: turn, EndedAt: ended, Promise: promise, StateP: map[string]float64{"done": 0.9}, Facts: TurnFacts{BackgroundRunning: true, BackgroundSource: "host", BackgroundTasks: 1}}); err != nil {
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
	if got[1].StateP["done"] != 0.9 || !got[1].Facts.BackgroundRunning || got[1].Facts.BackgroundSource != "host" || !got[1].JudgedAt.IsZero() {
		t.Fatalf("t1 read back as %+v", got[1])
	}
}

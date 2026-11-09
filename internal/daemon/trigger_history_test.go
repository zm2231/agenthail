package daemon

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestEveryWayATriggerRunEndsLeavesItsHistoryEntry(t *testing.T) {
	_, r, _, from, _ := daemonFixture(t)
	start := func(turn string) registry.TriggerRun {
		t.Helper()
		if err := r.RecordCompletion(from.ID, nil, []registry.TriggerRun{{Trigger: "flag", DedupeKey: "h:from:" + turn, SessionID: from.ID, TurnID: turn, Completion: json.RawMessage(`{}`)}}); err != nil {
			t.Fatal(err)
		}
		run, err := r.StartNextTriggerRun("flag")
		if err != nil || run == nil {
			t.Fatalf("run=%+v err=%v", run, err)
		}
		return *run
	}
	entryFor := func(turn string) *registry.HistoryEntry {
		t.Helper()
		history, err := r.ListHistory(50, from.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range history {
			if entry.Kind == "trigger" && entry.CompletionID == turn {
				return &entry
			}
		}
		return nil
	}

	finished := start("t1")
	if err := r.FinishTriggerRun(finished.ID, registry.TriggerOutcome{State: registry.TriggerFailed, ExitCode: 3, Error: "boom"}); err != nil {
		t.Fatal(err)
	}
	if entry := entryFor("t1"); entry == nil || entry.Result != "flag exited 3" || entry.Error != "boom" {
		t.Fatalf("a finished run's entry is %+v, want flag exited 3 with its error", entry)
	}

	interrupted := start("t2")
	if n, err := r.InterruptTriggerRuns(); err != nil || n != 1 {
		t.Fatalf("interrupted %d err=%v, want 1", n, err)
	}
	if stored, err := r.TriggerRun(interrupted.ID); err != nil || stored.State != registry.TriggerInterrupted {
		t.Fatalf("the run is %+v err=%v, want interrupted", stored, err)
	}
	if entry := entryFor("t2"); entry == nil || entry.Result != "flag interrupted" || entry.Error == "" {
		t.Fatalf("an interrupted run's entry is %+v, want flag interrupted with the reason", entry)
	}

	backlog := make([]registry.TriggerRun, 0, registry.MaxTriggerBacklog)
	for i := range registry.MaxTriggerBacklog {
		turn := fmt.Sprintf("w%d", i)
		backlog = append(backlog, registry.TriggerRun{Trigger: "flag", DedupeKey: "h:from:" + turn, SessionID: from.ID, TurnID: turn, Completion: json.RawMessage(`{}`)})
	}
	if err := r.RecordCompletion(from.ID, nil, backlog); err != nil {
		t.Fatal(err)
	}
	over := []registry.TriggerRun{{Trigger: "flag", DedupeKey: "h:from:t3", SessionID: from.ID, TurnID: "t3", Completion: json.RawMessage(`{}`)}}
	for range 2 {
		if err := r.RecordCompletion(from.ID, nil, over); err != nil {
			t.Fatal(err)
		}
	}
	if entry := entryFor("t3"); entry == nil || entry.Result != "flag skipped" || entry.Error == "" {
		t.Fatalf("a skipped run's entry is %+v, want flag skipped with the reason", entry)
	}
	history, err := r.ListHistory(200, from.ID)
	if err != nil {
		t.Fatal(err)
	}
	skipped := 0
	for _, entry := range history {
		if entry.Kind == "trigger" && entry.CompletionID == "t3" {
			skipped++
		}
	}
	if skipped != 1 {
		t.Fatalf("a completion recorded twice left %d skipped entries, want 1", skipped)
	}
}

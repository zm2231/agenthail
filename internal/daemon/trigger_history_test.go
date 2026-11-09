package daemon

import (
	"encoding/json"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestAFinishedTriggerRunAndItsHistoryEntryAreWrittenTogether(t *testing.T) {
	_, r, _, from, _ := daemonFixture(t)
	if err := r.RecordCompletion(from.ID, nil, []registry.TriggerRun{{Trigger: "flag", DedupeKey: "h:from:t1", SessionID: from.ID, TurnID: "t1", Completion: json.RawMessage(`{}`)}}); err != nil {
		t.Fatal(err)
	}
	run, err := r.StartNextTriggerRun("flag")
	if err != nil || run == nil {
		t.Fatalf("run=%+v err=%v", run, err)
	}
	triggerEntries := func() int {
		history, err := r.ListHistory(50, from.ID)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, entry := range history {
			if entry.Kind == "trigger" {
				n++
			}
		}
		return n
	}
	outcome := registry.TriggerOutcome{State: registry.TriggerSucceeded}
	if err := r.FinishTriggerRun(run.ID, outcome, registry.HistoryEntry{SessionID: from.ID}); err == nil {
		t.Fatal("a history entry with no kind was accepted")
	}
	if stored, err := r.TriggerRun(run.ID); err != nil || stored.State != registry.TriggerRunning || triggerEntries() != 0 {
		t.Fatalf("a failed history write left the run %+v (err %v) and %d entries; want it still running and none", stored, err, triggerEntries())
	}
	if err := r.FinishTriggerRun(run.ID, outcome, registry.HistoryEntry{Kind: "trigger", SessionID: from.ID, CompletionID: "t1", Result: "flag succeeded"}); err != nil {
		t.Fatal(err)
	}
	if stored, err := r.TriggerRun(run.ID); err != nil || stored.State != registry.TriggerSucceeded || triggerEntries() != 1 {
		t.Fatalf("the run is %+v (err %v) with %d entries; want succeeded with one", stored, err, triggerEntries())
	}
}

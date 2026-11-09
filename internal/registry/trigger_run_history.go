package registry

import "fmt"

func triggerRunHistory(run TriggerRun, outcome TriggerOutcome) HistoryEntry {
	result := fmt.Sprintf("%s %s", run.Trigger, outcome.State)
	if outcome.State == TriggerFailed && outcome.ExitCode > 0 {
		result = fmt.Sprintf("%s exited %d", run.Trigger, outcome.ExitCode)
	}
	if run.Test {
		result += " (test)"
	}
	return HistoryEntry{Kind: "trigger", SessionID: run.SessionID, CompletionID: run.TurnID, Result: result, Error: outcome.Error}
}

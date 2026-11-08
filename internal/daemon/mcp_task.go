package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/apitime"
	"github.com/zm2231/agenthail/internal/registry"
)

type mcpReportInput struct {
	mcpWrite
	Status string `json:"status" jsonschema:"done or failed."`
	Result string `json:"result" jsonschema:"What your spawner needs to know. The first report is your task's result; later ones only update its latest."`
}

func (d *Daemon) mcpReport(ctx context.Context, caller *mcpCaller, input mcpReportInput) (any, error) {
	state := strings.TrimSpace(input.Status)
	if state != registry.TaskDone && state != registry.TaskFailed {
		return nil, refuse("invalid", "status is done or failed.")
	}
	if strings.TrimSpace(input.Result) == "" {
		return nil, refuse("invalid", "result is required.")
	}
	task, found, err := d.Registry.TaskForChild(caller.session.ID)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, refuse("not_a_task", "This session was not started as a task, so it has nothing to report to.")
	}
	key, err := input.key("agenthail_report")
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return nil, err
	}
	reply := d.idempotentReply(sessionPrincipal(caller.session.ID).ID, key, body, mcpRequestHash(ctx), func([]byte) actionReply {
		return d.recordReport(caller.session.ID, task.ID, state, input.Result)
	})
	result, refusal := mcpActionOutcome(reply)
	d.recordMCPWrite(caller, "agenthail_report", caller.session.ID, input.ClientRequestID, refusal)
	if refusal != nil {
		return nil, refusal
	}
	return result, nil
}

func (d *Daemon) recordReport(child string, taskID int64, state, result string) actionReply {
	recorder := newActionRecorder()
	// The turn finished before this report is its baseline: only a later one is latest.
	baseline, _, err := d.Registry.RuntimeState(child)
	if err != nil {
		writeActionResult(recorder, actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error()))
		return recorder.reply()
	}
	settled, err := d.settleTask(child, registry.TaskSettlement{State: state, Kind: registry.TaskKindReported, Result: result, TurnID: baseline.CompletedTurnID})
	if err == nil && !settled {
		err = d.Registry.RecordTaskLatest(child, "", result, time.Now())
	}
	if err != nil {
		writeActionResult(recorder, actionResult{}, actionFailed(http.StatusInternalServerError, "internal_error", err.Error()))
		return recorder.reply()
	}
	d.settleWork(child, registry.WorkSettledTaskReported)
	writeActionResult(recorder, actionOK(map[string]any{"taskId": taskID, "settled": settled}), nil)
	return recorder.reply()
}

type mcpResultInput struct {
	TaskID int64  `json:"taskId,omitempty" jsonschema:"The taskId agenthail_create returned."`
	Target string `json:"target,omitempty" jsonschema:"Or the child's session id or @alias."`
}

type mcpTaskResult struct {
	TaskID    int64  `json:"taskId"`
	Child     string `json:"child"`
	State     string `json:"state"`
	Kind      string `json:"kind,omitempty"`
	Result    string `json:"result,omitempty"`
	SettledAt string `json:"settledAt,omitempty"`
	Latest    string `json:"latest,omitempty"`
	LatestAt  string `json:"latestAt,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Next      string `json:"next,omitempty"`
}

func (d *Daemon) mcpResult(ctx context.Context, caller *mcpCaller, input mcpResultInput) (any, error) {
	task, err := d.mcpReadableTask(caller, input)
	if err != nil {
		return nil, err
	}
	if task.State != registry.TaskRunning && task.Parent == caller.session.ID {
		if err := d.clearTaskWake(task.ID); err != nil {
			return nil, err
		}
	}
	return clippedTaskResult(d.refreshTaskLatest(ctx, task), mcpOutputLimit), nil
}

func (d *Daemon) mcpReadableTask(caller *mcpCaller, input mcpResultInput) (registry.Task, error) {
	task, err := d.mcpResultTask(caller, input)
	if err != nil || task.Parent == caller.session.ID {
		return task, err
	}
	if _, err := d.mcpReadableSession(caller, task.Child); err != nil {
		var refusal *mcpRefusal
		if errors.As(err, &refusal) {
			return task, errNoSuchTask
		}
		return task, err
	}
	return task, nil
}

func (d *Daemon) mcpResultTask(caller *mcpCaller, input mcpResultInput) (registry.Task, error) {
	var task registry.Task
	var found bool
	var err error
	target := strings.TrimSpace(input.Target)
	switch {
	case input.TaskID != 0 && target != "":
		return task, refuse("invalid", "Pass taskId or target, not both.")
	case input.TaskID != 0:
		task, found, err = d.Registry.TaskByID(input.TaskID)
	case target != "":
		child := target
		if alias, aliasErr := d.Registry.LookupAlias(strings.TrimPrefix(target, "@")); aliasErr == nil {
			child = alias
		}
		task, found, err = d.Registry.TaskForChild(child)
	default:
		return task, refuse("invalid", "taskId or target is required.")
	}
	if err != nil {
		return task, err
	}
	if !found {
		return task, errNoSuchTask
	}
	return task, nil
}

// One answer for a missing task and a foreign one, so ids can't be probed.
var errNoSuchTask = refuse("not_found", "No such task that you can read.")

func clippedTaskResult(task registry.Task, budget int) mcpTaskResult {
	out := mcpTaskResult{TaskID: task.ID, Child: task.Child, State: task.State, Kind: task.Kind}
	if !task.SettledAt.IsZero() {
		out.SettledAt = task.SettledAt.UTC().Format(time.RFC3339)
	}
	if !task.LatestAt.IsZero() {
		out.LatestAt = task.LatestAt.UTC().Format(time.RFC3339)
	}
	// JSON escaping can more than double the text, so the fit is measured encoded.
	for limit := budget; ; limit = limit * 3 / 4 {
		out.Result, out.Latest = clipUTF8(task.Result, limit*3/4), clipUTF8(task.Latest, limit/8)
		out.Truncated = len(out.Result) < len(task.Result) || len(out.Latest) < len(task.Latest)
		out.Next = ""
		if out.Truncated {
			out.Next = "The text is clipped; agenthail_read on the child has all of it."
		}
		encoded, err := apitime.Marshal(out)
		if err != nil || len(encoded) <= budget || limit == 0 {
			return out
		}
	}
}

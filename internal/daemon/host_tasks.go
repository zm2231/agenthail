package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	hostTasksPath     = "/api/v1/host-tasks"
	hostTaskPullLimit = 16
	hostTaskPullWait  = 10 * time.Second
)

type hostTask struct {
	ChildID      string        `json:"childId"`
	State        string        `json:"state"`
	Kind         string        `json:"kind,omitempty"`
	Result       string        `json:"result,omitempty"`
	ResultTurnID string        `json:"resultTurnId,omitempty"`
	Latest       string        `json:"latest,omitempty"`
	LatestTurnID string        `json:"latestTurnId,omitempty"`
	LatestAtMS   int64         `json:"latestAtMs,omitempty"`
	Stalled      bool          `json:"stalled,omitempty"`
	GoalStop     *hostGoalStop `json:"goalStop,omitempty"`
}

type hostGoalStop struct {
	Status string `json:"status"`
	Reason string `json:"reason,omitempty"`
	Key    string `json:"key"`
}

func (h hostTask) task() registry.Task {
	task := registry.Task{Child: h.ChildID, State: h.State, Kind: h.Kind, Result: h.Result, ResultTurnID: h.ResultTurnID, Latest: h.Latest, LatestTurnID: h.LatestTurnID, Stalled: h.Stalled}
	if h.LatestAtMS > 0 {
		task.LatestAt = time.UnixMilli(h.LatestAtMS)
	}
	return task
}

func (d *Daemon) apiHostTasksHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET for host tasks.")
		return
	}
	hostID, err := d.boundHost(r.Context())
	if err != nil {
		writeActionResult(w, actionResult{}, err)
		return
	}
	children := r.URL.Query()["child"]
	if len(children) > hostTaskPullLimit {
		writeAPIError(w, http.StatusBadRequest, "invalid_request", fmt.Sprintf("Ask for at most %d tasks at once.", hostTaskPullLimit))
		return
	}
	tasks, err := d.Registry.HostTasks(hostID, children)
	if err != nil {
		writeAPIError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	held := make([]hostTask, 0, len(tasks))
	for _, task := range tasks {
		task = d.refreshTaskLatest(r.Context(), task)
		record := hostTask{ChildID: task.Child, State: task.State, Kind: task.Kind, Result: task.Result, ResultTurnID: task.ResultTurnID, Latest: task.Latest, LatestTurnID: task.LatestTurnID, Stalled: task.Stalled}
		if !task.LatestAt.IsZero() {
			record.LatestAtMS = task.LatestAt.UnixMilli()
		}
		if task.State == registry.TaskRunning {
			if goal := d.stoppedTaskGoal(r.Context(), task); goal != nil {
				record.GoalStop = &hostGoalStop{Status: goal.Status, Reason: goal.Reason, Key: goalStopNoticeKey(task.Child, goal)}
			}
		}
		held = append(held, record)
	}
	writeDashboardJSON(w, http.StatusOK, map[string]any{"tasks": held})
}

func (d *Daemon) startHostTask(caller *mcpCaller, host *pairedHost, message string, result json.RawMessage) (json.RawMessage, error) {
	var started map[string]any
	if err := json.Unmarshal(result, &started); err != nil {
		return nil, err
	}
	var reply struct {
		TaskID    int64           `json:"taskId"`
		SessionID string          `json:"sessionId"`
		Session   surface.Session `json:"session"`
	}
	if err := json.Unmarshal(result, &reply); err != nil {
		return nil, err
	}
	child := reply.Session
	if child.ID == "" {
		child.ID = reply.SessionID
	}
	if _, raw, qualified := hosts.Split(child.ID); qualified {
		child.ID = raw
	}
	if reply.TaskID == 0 || child.ID == "" {
		return nil, refuse("host_unsupported", fmt.Sprintf("%s started the session but did not record it as a task; it runs an older Agenthail, so update it to start tasks there.", host.record.Name))
	}
	task, err := d.Registry.CreateHostTask(host.record.ID, caller.session.ID, child, message, time.Now())
	if err != nil {
		return nil, fmt.Errorf("%s started session %s, but its task could not be recorded here: %w; do not retry automatically", host.record.Name, child.ID, err)
	}
	started["taskId"] = task.ID
	return json.Marshal(started)
}

func (d *Daemon) pullHostTasks(ctx context.Context, host *pairedHost, children []string) error {
	for len(children) > 0 {
		batch := children[:min(len(children), hostTaskPullLimit)]
		children = children[len(batch):]
		var answer struct {
			Tasks []hostTask `json:"tasks"`
		}
		pullCtx, cancel := context.WithTimeout(ctx, hostTaskPullWait)
		err := host.client.GetJSON(pullCtx, hostTasksPath, url.Values{"child": batch}, &answer)
		cancel()
		if err != nil {
			return fmt.Errorf("read tasks on host %s: %w", host.record.Name, err)
		}
		for _, held := range answer.Tasks {
			d.applyHostTask(host, held)
		}
	}
	return nil
}

func (d *Daemon) pullRunningHostTasks(ctx context.Context, host *pairedHost) {
	children, err := d.Registry.HostTaskChildren(host.record.ID, true)
	if err == nil && len(children) > 0 {
		err = d.pullHostTasks(ctx, host, children)
	}
	if err != nil {
		d.log.Printf("host %s tasks: %s", host.record.Name, err)
	}
}

func (d *Daemon) pullChangedHostTask(ctx context.Context, host *pairedHost, payload json.RawMessage) {
	var changed struct {
		ChildID string `json:"childId"`
	}
	if json.Unmarshal(payload, &changed) != nil || changed.ChildID == "" {
		return
	}
	if owner, err := d.Registry.SessionHost(changed.ChildID); err != nil || owner != host.record.ID {
		return
	}
	if _, found, err := d.Registry.TaskForChild(changed.ChildID); err != nil || !found {
		return
	}
	if err := d.pullHostTasks(ctx, host, []string{changed.ChildID}); err != nil {
		d.log.Printf("host %s tasks: %s", host.record.Name, err)
	}
}

func (d *Daemon) applyHostTask(host *pairedHost, held hostTask) {
	task := held.task()
	settled, err := d.Registry.ApplyHostTask(host.record.ID, task, time.Now())
	if err != nil {
		d.log.Printf("task for %s on host %s: %s", d.resolveDisplay(held.ChildID), host.record.Name, err)
		return
	}
	if settled {
		d.publishTaskSettled(held.ChildID, held.State, held.Kind)
	}
	if d.hostGoalStops.set(held.ChildID, held.GoalStop) {
		d.publishEvent(taskEventGoalStopped, held.ChildID, nil)
	}
}

func (d *Daemon) refreshHostTask(ctx context.Context, task registry.Task, hostID string) (registry.Task, string) {
	host, found := d.pairedHost(hostID)
	if !found {
		return task, ""
	}
	if err := d.pullHostTasks(ctx, host, []string{task.Child}); err != nil {
		if task.State != registry.TaskRunning {
			return task, ""
		}
		return task, fmt.Sprintf("%s is unreachable, so this is what it last said; the result arrives when it reconnects.", host.record.Name)
	}
	fresh, found, err := d.Registry.TaskForChild(task.Child)
	if err != nil || !found {
		return task, ""
	}
	return fresh, ""
}

// A goal is read where its session runs, so a remote child's stop is what its host last reported.
type hostGoalStops struct {
	mu    sync.Mutex
	stops map[string]hostGoalStop
}

func (s *hostGoalStops) set(child string, stop *hostGoalStop) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if stop == nil {
		delete(s.stops, child)
		return false
	}
	if s.stops == nil {
		s.stops = map[string]hostGoalStop{}
	}
	prior, found := s.stops[child]
	s.stops[child] = *stop
	return !found || prior.Key != stop.Key
}

func (s *hostGoalStops) get(child string) (hostGoalStop, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	stop, found := s.stops[child]
	return stop, found
}

func (d *Daemon) taskChildHost(child string) string {
	owner, err := d.Registry.SessionHost(child)
	if err != nil {
		return ""
	}
	return owner
}

func (d *Daemon) flushTaskChange() {
	if err := d.catalog.flushCommitted(); err != nil {
		d.log.Printf("publish a task change: %s", err)
	}
}

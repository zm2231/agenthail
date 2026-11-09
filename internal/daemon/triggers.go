package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/triggers"
)

const triggerSlots = 2

var errNoSuchTrigger = errors.New("no such trigger")

type triggerService struct {
	path   string
	runner triggers.Runner
	slots  chan struct{}

	mu       sync.Mutex
	config   triggers.Config
	loadErr  error
	loadedAt time.Time
	active   map[string]bool
	again    map[string]bool
	drained  func(name string)
}

func newTriggerService() *triggerService {
	dir := filepath.Dir(PidFilePath())
	return &triggerService{path: TriggersConfigPath(), runner: triggers.Runner{WorkDir: dir}, slots: make(chan struct{}, triggerSlots), active: map[string]bool{}, again: map[string]bool{}}
}

func TriggersConfigPath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "triggers.json")
}

func (d *Daemon) startTriggers() {
	if interrupted, err := d.Registry.InterruptTriggerRuns(); err != nil {
		d.log.Printf("mark interrupted trigger runs: %s", err)
	} else if interrupted > 0 {
		d.log.Printf("%d trigger run(s) were running when the daemon stopped; marked interrupted", interrupted)
	}
	d.reloadTriggers()
}

func (d *Daemon) reloadTriggers() error {
	t := d.triggers
	config, err := triggers.Load(t.path)
	t.mu.Lock()
	t.loadedAt = time.Now()
	t.loadErr = err
	if err == nil {
		t.config = config
	} else {
		t.config = triggers.Config{}
	}
	t.mu.Unlock()
	if err != nil {
		d.log.Printf("triggers disabled: %s", err)
		return err
	}
	d.dispatchTriggers()
	return nil
}

func (t *triggerService) trigger(name string) (triggers.Trigger, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, trigger := range t.config.Triggers {
		if trigger.Name == name {
			return trigger, true
		}
	}
	return triggers.Trigger{}, false
}

func (t *triggerService) matching(c triggers.Completion) []triggers.Trigger {
	t.mu.Lock()
	defer t.mu.Unlock()
	var matched []triggers.Trigger
	for _, trigger := range t.config.Triggers {
		if trigger.Matches(c) {
			matched = append(matched, trigger)
		}
	}
	return matched
}

// Only this host's observer calls recordCompletion, and it watches only
// sessions this host owns, so a paired host's turn never fires a trigger here.
func (d *Daemon) recordCompletion(session *surface.Session, turnID string, failed bool, notice *turnCompletedNotice) {
	completion := d.triggerCompletion(session, turnID, failed)
	encoded, _ := json.Marshal(completion)
	var runs []registry.TriggerRun
	for _, trigger := range d.triggers.matching(completion) {
		runs = append(runs, registry.TriggerRun{Trigger: trigger.Name, DedupeKey: completion.DedupeKey(), SessionID: session.ID, TurnID: turnID, Completion: encoded})
	}
	var recorded *registry.TurnNotice
	if notice != nil {
		recorded = &registry.TurnNotice{TurnID: turnID, Outcome: notice.outcome, RequestID: notice.requestID, At: time.Now()}
	}
	if recorded == nil && len(runs) == 0 {
		return
	}
	if err := d.Registry.RecordCompletion(session.ID, recorded, runs); err != nil {
		d.log.Printf("record completion %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if recorded != nil {
		d.requestCatalogDiscovery()
	}
	if len(runs) > 0 {
		d.dispatchTriggers()
	}
}

func (d *Daemon) triggerCompletion(session *surface.Session, turnID string, failed bool) triggers.Completion {
	host := d.hostIdentity()
	alias, _ := d.Registry.ReverseAlias(session.ID)
	completion := triggers.Completion{HostID: host.ID, HostName: host.Name, SessionID: session.ID, Alias: alias, Surface: string(session.Surface), Name: session.Name, Cwd: session.Cwd, TurnID: turnID, Outcome: triggers.OutcomeFinished, CompletedAt: time.Now(), Delegated: session.Delegated()}
	if failed {
		completion.Outcome = triggers.OutcomeFailed
	}
	if project, err := d.sessionProject(*session); err == nil && project != nil {
		completion.ProjectID, completion.ProjectName = project.ID, project.DisplayName
	}
	return completion
}

func (d *Daemon) dispatchTriggers() {
	t := d.triggers
	t.mu.Lock()
	broken := t.loadErr != nil
	t.mu.Unlock()
	if broken {
		return
	}
	names, err := d.Registry.PendingTriggers()
	if err != nil {
		d.log.Printf("list pending trigger runs: %s", err)
		return
	}
	for _, name := range names {
		trigger, configured := t.trigger(name)
		if !configured {
			continue
		}
		t.mu.Lock()
		if t.active[name] {
			t.again[name] = true
			t.mu.Unlock()
			continue
		}
		t.active[name] = true
		t.mu.Unlock()
		if !d.background.start(func(ctx context.Context) { d.drainTrigger(ctx, trigger) }) {
			t.mu.Lock()
			delete(t.active, name)
			t.mu.Unlock()
		}
	}
}

// drainTrigger runs one trigger's pending runs in order, holding one of the
// host's slots for each. A dispatch that finds it draining marks it to look
// again; the drain checks that mark and gives up the trigger under one lock,
// so a run queued as it finishes is never left waiting.
func (d *Daemon) drainTrigger(ctx context.Context, trigger triggers.Trigger) {
	t := d.triggers
	defer t.release(trigger.Name)
	for ctx.Err() == nil {
		select {
		case t.slots <- struct{}{}:
		case <-ctx.Done():
			return
		}
		ran := d.runNextTrigger(ctx, trigger)
		<-t.slots
		if !ran && !t.lookAgain(trigger.Name) {
			return
		}
	}
}

func (t *triggerService) lookAgain(name string) bool {
	if t.drained != nil {
		t.drained(name)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.again[name] {
		delete(t.again, name)
		return true
	}
	delete(t.active, name)
	return false
}

func (t *triggerService) release(name string) {
	t.mu.Lock()
	delete(t.active, name)
	delete(t.again, name)
	t.mu.Unlock()
}

func (d *Daemon) runNextTrigger(ctx context.Context, trigger triggers.Trigger) bool {
	run, err := d.Registry.StartNextTriggerRun(trigger.Name)
	if err != nil {
		d.log.Printf("start trigger %s: %s", trigger.Name, err)
		return false
	}
	if run == nil {
		return false
	}
	var completion triggers.Completion
	if err := json.Unmarshal(run.Completion, &completion); err != nil {
		d.finishTriggerRun(*run, registry.TriggerOutcome{State: registry.TriggerFailed, ExitCode: -1, Error: fmt.Sprintf("read the recorded completion: %s", err)})
		return true
	}
	result := d.triggers.runner.Execute(ctx, trigger, completion, run.Test)
	outcome := registry.TriggerOutcome{State: result.State, ExitCode: result.ExitCode, Signal: result.Signal, Stdout: result.Stdout, Stderr: result.Stderr, Error: result.Error}
	if ctx.Err() != nil && result.State != triggers.StateTimedOut {
		outcome.State = registry.TriggerInterrupted
	}
	d.finishTriggerRun(*run, outcome)
	return true
}

func (d *Daemon) finishTriggerRun(run registry.TriggerRun, outcome registry.TriggerOutcome) {
	result := fmt.Sprintf("%s %s", run.Trigger, outcome.State)
	if outcome.State == registry.TriggerFailed && outcome.ExitCode > 0 {
		result = fmt.Sprintf("%s exited %d", run.Trigger, outcome.ExitCode)
	}
	if run.Test {
		result += " (test)"
	}
	history := registry.HistoryEntry{Kind: "trigger", SessionID: run.SessionID, CompletionID: run.TurnID, Result: result, Error: outcome.Error}
	if err := d.Registry.FinishTriggerRun(run.ID, outcome, history); err != nil {
		d.log.Printf("finish trigger run %d: %s", run.ID, err)
		return
	}
	d.publishEvent("trigger.finished", run.SessionID, map[string]any{"runId": run.ID, "trigger": run.Trigger, "state": outcome.State})
}

func (d *Daemon) testTrigger(name string) (registry.TriggerRun, error) {
	if _, ok := d.triggers.trigger(name); !ok {
		return registry.TriggerRun{}, fmt.Errorf("%w: %q", errNoSuchTrigger, name)
	}
	host := d.hostIdentity()
	completion := triggers.Completion{HostID: host.ID, HostName: host.Name, SessionID: "test", Surface: "codex", Name: "trigger test", TurnID: uuid.NewString(), Outcome: triggers.OutcomeFinished, CompletedAt: time.Now()}
	encoded, _ := json.Marshal(completion)
	run := registry.TriggerRun{Trigger: name, DedupeKey: "test:" + completion.TurnID, TurnID: completion.TurnID, Completion: encoded, Test: true}
	if err := d.Registry.RecordCompletion("", nil, []registry.TriggerRun{run}); err != nil {
		return registry.TriggerRun{}, err
	}
	runs, err := d.Registry.ListTriggerRuns(registry.TriggerRunFilter{Trigger: name, Limit: 1})
	if err != nil || len(runs) == 0 || runs[0].DedupeKey != run.DedupeKey {
		return registry.TriggerRun{}, errors.Join(err, errors.New("the test run was not recorded"))
	}
	d.dispatchTriggers()
	return runs[0], nil
}

type triggerStatus struct {
	Path     string               `json:"path"`
	Loaded   bool                 `json:"loaded"`
	Error    string               `json:"error,omitempty"`
	LoadedAt time.Time            `json:"loadedAt"`
	Triggers []triggerStatusEntry `json:"triggers"`
}

type triggerStatusEntry struct {
	Name      string               `json:"name"`
	Action    string               `json:"action"`
	Run       []string             `json:"run,omitempty"`
	Touch     string               `json:"touch,omitempty"`
	When      triggers.When        `json:"when"`
	Delegated bool                 `json:"delegated,omitempty"`
	Timeout   int                  `json:"timeoutSeconds"`
	LastRun   *registry.TriggerRun `json:"lastRun,omitempty"`
}

func (d *Daemon) triggerStatus() (triggerStatus, error) {
	t := d.triggers
	t.mu.Lock()
	status := triggerStatus{Path: t.path, Loaded: t.loadErr == nil, LoadedAt: t.loadedAt, Triggers: []triggerStatusEntry{}}
	if t.loadErr != nil {
		status.Error = t.loadErr.Error()
	}
	configured := append([]triggers.Trigger(nil), t.config.Triggers...)
	t.mu.Unlock()
	for _, trigger := range configured {
		entry := triggerStatusEntry{Name: trigger.Name, Action: "run", Run: trigger.Run, Touch: trigger.Touch, When: trigger.When, Delegated: trigger.Delegated, Timeout: int(trigger.Timeout.Seconds())}
		if trigger.Touch != "" {
			entry.Action = "touch"
		}
		runs, err := d.Registry.ListTriggerRuns(registry.TriggerRunFilter{Trigger: trigger.Name, Limit: 1})
		if err != nil {
			return status, err
		}
		if len(runs) > 0 {
			entry.LastRun = &runs[0]
		}
		status.Triggers = append(status.Triggers, entry)
	}
	return status, nil
}

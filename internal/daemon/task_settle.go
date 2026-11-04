package daemon

import (
	"context"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

const (
	taskGoalRecheck    = 30 * time.Second
	taskChildEndedNote = "The child's session ended before it finished."
)

func (d *Daemon) settleTask(child string, settlement registry.TaskSettlement) (bool, error) {
	settled, err := d.Registry.SettleTask(child, settlement, time.Now())
	if err != nil || !settled {
		return settled, err
	}
	d.publishTaskSettled(child, settlement.State, settlement.Kind)
	return true, nil
}

func (d *Daemon) publishTaskSettled(child, state, kind string) {
	d.publishEvent(taskEventSettled, child, map[string]string{"state": state, "kind": kind})
}

// finishedTaskTurn reads a settlement from one observation, so the text always
// belongs to the turn it names. It holds whatever the observation shows.
func finishedTaskTurn(observation *surface.TurnObservation) (registry.TaskSettlement, bool) {
	settlement := registry.TaskSettlement{State: registry.TaskDone, Kind: registry.TaskKindTurn}
	if observation == nil || observation.Status == surface.StatusBusy || observation.ActiveTurnID != "" || observation.TurnUnconfirmed || surface.BackgroundRunning(observation.BackgroundTasks) {
		return settlement, false
	}
	if observation.TerminalTurnID != "" && observation.TerminalTurnID != observation.CompletedTurnID {
		settlement.TurnID = observation.TerminalTurnID
		if observation.TerminalError != "" {
			settlement.State, settlement.Result = registry.TaskFailed, observation.TerminalError
		}
		return settlement, true
	}
	if observation.CompletedTurnID == "" || observation.Reply == nil || !observation.Reply.Done {
		return settlement, false
	}
	settlement.TurnID, settlement.Result = observation.CompletedTurnID, observation.Reply.Text
	if observation.Reply.Error != "" {
		settlement.State, settlement.Result = registry.TaskFailed, observation.Reply.Error
	}
	return settlement, true
}

// settleTaskFromObservation runs on every pass for a running task, so a turn
// settles however long ago it ended and whatever was down when it did.
func (d *Daemon) settleTaskFromObservation(ctx context.Context, adapter surface.Surface, session *surface.Session, observation *surface.TurnObservation) {
	task, found, err := d.Registry.TaskForChild(session.ID)
	if err != nil {
		d.log.Printf("task for %s: %s", d.resolveDisplay(session.ID), err)
		return
	}
	if !found || task.State != registry.TaskRunning {
		return
	}
	if observation.TurnUnconfirmed != task.Stalled {
		if err := d.Registry.MarkTaskStalled(session.ID, observation.TurnUnconfirmed); err != nil {
			d.log.Printf("task stalled %s: %s", d.resolveDisplay(session.ID), err)
		}
	}
	settlement, finished := finishedTaskTurn(observation)
	if !finished {
		return
	}
	if surface.EffectiveCapabilities(adapter, session).Goal {
		var open bool
		if settlement, open = d.settlementUnderGoal(ctx, adapter, session, settlement); open {
			return
		}
	}
	if _, err := d.settleTask(session.ID, settlement); err != nil {
		d.log.Printf("settle task %s: %s", d.resolveDisplay(session.ID), err)
	}
}

// A goal read as complete is followed by a fresh observation: the turn that
// completed it exists by then, so an idle child has finished that turn. An
// unreadable goal holds too, since a settlement can't be taken back.
func (d *Daemon) settlementUnderGoal(ctx context.Context, adapter surface.Surface, session *surface.Session, settlement registry.TaskSettlement) (registry.TaskSettlement, bool) {
	if !d.taskGoals.due(session.ID, settlement.TurnID, time.Now()) {
		return settlement, true
	}
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	goal, err := adapter.GoalGet(readCtx, session)
	if err == nil && (goal == nil || goal.CreatedAt.IsZero()) {
		return settlement, false
	}
	if err != nil || goal.Status != surface.GoalStatusComplete {
		if err == nil && goalStopped(goal.Status) {
			d.publishEvent(taskEventGoalStopped, session.ID, nil)
		}
		if err := d.Registry.RecordTaskTurn(session.ID, settlement.TurnID, settlement.State, settlement.Result); err != nil {
			d.log.Printf("task turn %s: %s", d.resolveDisplay(session.ID), err)
		}
		return settlement, true
	}
	fresh, err := adapter.Observe(readCtx, session)
	if err != nil {
		return settlement, true
	}
	final, finished := finishedTaskTurn(fresh)
	final.Kind = registry.TaskKindGoal
	return final, !finished
}

// A child off the catalog that can't be resumed has nothing more to say; the
// turn a goal held open is the best result left.
func (d *Daemon) settleGoneTask(session *surface.Session) {
	adapter, err := d.adapters().For(session)
	if err != nil || childResumable(adapter, session) {
		return
	}
	task, found, err := d.Registry.TaskForChild(session.ID)
	if err != nil || !found {
		return
	}
	settlement := registry.TaskSettlement{State: registry.TaskFailed, Kind: registry.TaskKindTurn, Result: taskChildEndedNote}
	if task.LastTurn != "" {
		settlement.State, settlement.Result, settlement.TurnID = task.LastState, task.LastText, task.LastTurn
	}
	if _, err := d.settleTask(session.ID, settlement); err != nil {
		d.log.Printf("settle task %s: %s", d.resolveDisplay(session.ID), err)
	}
}

func childResumable(adapter surface.Surface, session *surface.Session) bool {
	keeper, ok := adapter.(surface.EndedConversations)
	if !ok {
		return false
	}
	resumable, _ := keeper.EndedConversation(session.Transcript)
	return resumable
}

// taskGoalReads paces goal reads: at once for a newly finished turn, otherwise
// every taskGoalRecheck.
type taskGoalReads struct {
	mu    sync.Mutex
	reads map[string]taskGoalRead
}

type taskGoalRead struct {
	turn string
	at   time.Time
}

func (r *taskGoalReads) due(child, turn string, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	last, found := r.reads[child]
	if found && last.turn == turn && now.Sub(last.at) < taskGoalRecheck {
		return false
	}
	if r.reads == nil {
		r.reads = map[string]taskGoalRead{}
	}
	r.reads[child] = taskGoalRead{turn: turn, at: now}
	return true
}

func (r *taskGoalReads) retain(running map[string]bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for child := range r.reads {
		if _, found := running[child]; !found {
			delete(r.reads, child)
		}
	}
}

// refreshTaskLatest records a turn that finished after the task settled. Settled
// children aren't watched, so this is the one place that sees it; a failed read
// leaves what is stored.
func (d *Daemon) refreshTaskLatest(ctx context.Context, task registry.Task) registry.Task {
	if task.State == registry.TaskRunning || task.State == registry.TaskCancelled {
		return task
	}
	session, err := d.Registry.Session(task.Child)
	if err != nil {
		return task
	}
	adapter, err := d.adapters().For(session)
	if err != nil {
		return task
	}
	readCtx, cancel := context.WithTimeout(ctx, surfaceOperationTimeout)
	defer cancel()
	observation, err := adapter.Observe(readCtx, session)
	if err != nil || observation == nil || observation.TurnUnconfirmed || observation.Reply == nil || !observation.Reply.Done {
		return task
	}
	turn := observation.CompletedTurnID
	if turn == "" || turn == task.ResultTurnID || turn == task.LatestTurnID {
		return task
	}
	text := observation.Reply.Text
	if observation.Reply.Error != "" {
		text = observation.Reply.Error
	}
	if err := d.Registry.RecordTaskLatest(task.Child, turn, text, time.Now()); err != nil {
		d.log.Printf("task latest for %s: %s", d.resolveDisplay(task.Child), err)
		return task
	}
	if fresh, found, err := d.Registry.TaskForChild(task.Child); err == nil && found {
		return fresh
	}
	return task
}

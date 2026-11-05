package daemon

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type taskRequestRead struct {
	waitingID string
	reader    string
}

const (
	taskEventSettled     = "task.settled"
	taskEventRequest     = "task.request"
	taskEventGoalStopped = "task.goal_stopped"
)

func taskWakeKey(id int64) string {
	return fmt.Sprintf("task-settled-%d", id)
}

// deliverTaskWakes runs every observer pass, so a settlement from any path, or
// one the daemon stopped right after, still gets its notice. The delivery key
// keeps it to one.
func (d *Daemon) deliverTaskWakes() {
	tasks, err := d.Registry.TasksToWake()
	if err != nil {
		d.log.Printf("tasks to wake: %s", err)
		return
	}
	for _, task := range tasks {
		if err := d.wakeTaskParent(task); err != nil {
			d.log.Printf("wake the parent of task %d: %s", task.ID, err)
		}
	}
}

func (d *Daemon) wakeTaskParent(task registry.Task) error {
	if _, err := d.Registry.Session(task.Child); err != nil {
		return err
	}
	d.taskNoticeMu.Lock()
	defer d.taskNoticeMu.Unlock()
	current, found, err := d.Registry.TaskForChild(task.Child)
	if err != nil || !found || !current.Wake {
		return err
	}
	message := fmt.Sprintf("[agenthail] Task %d (%s) is %s. Read it with agenthail_result {taskId: %d}.", task.ID, d.answerTarget(task.Child), task.State, task.ID)
	if _, err := d.sendNotice(noticeRecipient{sessionID: task.Parent, role: "spawner"}, message, taskWakeKey(task.ID)); err != nil {
		return err
	}
	return d.Registry.ClearTaskWake(task.ID)
}

// clearTaskWake is the parent having read the result: a notice not yet delivered has nothing to add.
func (d *Daemon) clearTaskWake(id int64) error {
	d.taskNoticeMu.Lock()
	defer d.taskNoticeMu.Unlock()
	if err := d.Registry.ClearTaskWake(id); err != nil {
		return err
	}
	_, err := d.Registry.CancelPendingByKey(taskWakeKey(id))
	return err
}

// A wait wakes as soon as a request is published, usually before the announcer has queued its notice.
func (d *Daemon) noteWaitReadRequest(child, reader string, waiting surface.Waiting) {
	d.taskNoticeMu.Lock()
	defer d.taskNoticeMu.Unlock()
	if d.taskRequestsRead == nil {
		d.taskRequestsRead = map[string]taskRequestRead{}
	}
	d.taskRequestsRead[child] = taskRequestRead{waitingID: waiting.ID, reader: reader}
	d.withdrawWaitingNotice(child, waiting)
}

func (d *Daemon) retainTaskRequestsRead(running map[string]bool) {
	d.taskNoticeMu.Lock()
	defer d.taskNoticeMu.Unlock()
	for child := range d.taskRequestsRead {
		if _, found := running[child]; !found {
			delete(d.taskRequestsRead, child)
		}
	}
}

func (d *Daemon) publishTaskRequest(child string) {
	if task, found, err := d.Registry.TaskForChild(child); err == nil && found && task.State == registry.TaskRunning {
		d.publishEvent(taskEventRequest, child, nil)
	}
}

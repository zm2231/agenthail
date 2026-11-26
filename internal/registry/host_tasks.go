package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// CreateHostTask records a task whose child runs on a paired host. The child's
// row is written here before the host's catalog brings it, since the task
// refers to it from the start.
func (r *Registry) CreateHostTask(hostID, parent string, child surface.Session, text string, at time.Time) (Task, error) {
	if hostID == "" || parent == "" || child.ID == "" {
		return Task{}, fmt.Errorf("a task on a paired host needs its host, parent and child")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return Task{}, err
	}
	defer tx.Rollback()
	var paired bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM hosts WHERE host_id=?)`, hostID).Scan(&paired); err != nil {
		return Task{}, err
	}
	if !paired {
		return Task{}, fmt.Errorf("host %s is not paired", hostID)
	}
	var owner string
	err = tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, child.ID).Scan(&owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(`INSERT INTO sessions(id,host_id,surface,name,cwd,status,runtime_kind) VALUES(?,?,?,?,?,?,?)`,
			child.ID, hostID, string(child.Surface), child.Name, child.Cwd, string(child.Status), string(child.RuntimeKind)); err != nil {
			return Task{}, err
		}
	case err != nil:
		return Task{}, err
	case owner != hostID:
		return Task{}, SessionOwnedError{SessionID: child.ID, HostID: owner}
	}
	if _, err := tx.Exec(`INSERT INTO tasks(parent_session_id,child_session_id,task,created_at_ms) VALUES(?,?,?,?) ON CONFLICT(child_session_id) DO NOTHING`, parent, child.ID, clipTaskText(text, taskTextLimit), at.UnixMilli()); err != nil {
		return Task{}, err
	}
	task, err := scanTask(tx.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE child_session_id=?`, child.ID))
	if err != nil {
		return Task{}, err
	}
	return task, tx.Commit()
}

// HostTasks are the tasks among children that a session on hostID started:
// one this host mirrors from it, or one that stands in for it.
func (r *Registry) HostTasks(hostID string, children []string) ([]Task, error) {
	if hostID == "" || len(children) == 0 {
		return []Task{}, nil
	}
	standIn := surface.UnrepliableSender(hostID, "", "").ID
	args := []any{hostID, len(standIn), standIn}
	for _, child := range children {
		args = append(args, child)
	}
	return queryRows(r.read, []Task{}, scanTask, `SELECT `+prefixedTaskColumns("t")+` FROM tasks t JOIN sessions p ON p.id=t.parent_session_id
		WHERE (p.host_id=? OR substr(p.id,1,?)=?) AND t.child_session_id IN (?`+strings.Repeat(",?", len(children)-1)+`) ORDER BY t.id`, args...)
}

// HostTaskChildren are the children on hostID of tasks started here. A
// settled one stays among them, since a later report on that host updates it.
func (r *Registry) HostTaskChildren(hostID string) ([]string, error) {
	return queryRows(r.read, []string{}, scanString, `SELECT t.child_session_id FROM tasks t JOIN sessions s ON s.id=t.child_session_id
		WHERE s.host_id=? AND t.state<>'cancelled' ORDER BY t.id`, hostID)
}

// ApplyHostTask brings a task started here up to what the host running its
// child holds. It reports whether the task settled.
func (r *Registry) ApplyHostTask(hostID string, held Task, at time.Time) (bool, error) {
	local, found, err := r.TaskForChild(held.Child)
	if err != nil || !found {
		return false, err
	}
	if owner, err := r.SessionHost(held.Child); err != nil || owner != hostID {
		return false, err
	}
	settled := false
	switch {
	case held.State == TaskRunning:
		return false, r.MarkTaskStalled(held.Child, held.Stalled)
	case held.State == TaskCancelled:
		return local.State == TaskRunning, r.CancelTask(held.Child, at)
	case local.State == TaskRunning:
		settled, err = r.SettleTask(held.Child, TaskSettlement{State: held.State, Kind: held.Kind, Result: held.Result, TurnID: held.ResultTurnID}, at)
		if err != nil {
			return false, err
		}
	}
	if held.Latest == "" || held.Latest == local.Latest && held.LatestTurnID == local.LatestTurnID {
		return settled, nil
	}
	latestAt := held.LatestAt
	if latestAt.IsZero() {
		latestAt = at
	}
	_, err = r.db.Exec(`UPDATE tasks SET latest=?,latest_turn_id=?,latest_at_ms=? WHERE child_session_id=? AND state IN ('done','failed')`, clipTaskText(held.Latest, taskResultLimit), held.LatestTurnID, latestAt.UnixMilli(), held.Child)
	return settled, err
}

func (r *Registry) CancelTask(child string, at time.Time) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := cancelTaskTx(tx, child, at); err != nil {
		return err
	}
	return tx.Commit()
}

func prefixedTaskColumns(alias string) string {
	columns := strings.Split(taskColumns, ",")
	for index, column := range columns {
		columns[index] = alias + "." + column
	}
	return strings.Join(columns, ",")
}

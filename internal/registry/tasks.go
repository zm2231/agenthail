package registry

import (
	"database/sql"
	"errors"
	"time"
	"unicode/utf8"
)

const (
	TaskRunning   = "running"
	TaskDone      = "done"
	TaskFailed    = "failed"
	TaskCancelled = "cancelled"

	TaskKindReported = "reported"
	TaskKindGoal     = "goal"
	TaskKindTurn     = "turn"

	taskTextLimit   = 2 << 10
	taskResultLimit = 64 << 10
)

type Task struct {
	ID           int64
	Parent       string
	Child        string
	Text         string
	State        string
	Result       string
	Kind         string
	ResultTurnID string
	Latest       string
	LatestTurnID string
	LatestAt     time.Time
	LastTurn     string
	LastState    string
	LastText     string
	Stalled      bool
	Wake         bool
	CreatedAt    time.Time
	SettledAt    time.Time
}

type TaskSettlement struct {
	State  string
	Kind   string
	Result string
	TurnID string
}

const taskColumns = `id,parent_session_id,child_session_id,task,state,result,result_kind,result_turn_id,latest,latest_turn_id,latest_at_ms,last_turn_id,last_state,last_text,stalled,wake,created_at_ms,settled_at_ms`

func scanTask(row rowScanner) (Task, error) {
	var task Task
	var latestAt, createdAt, settledAt int64
	err := row.Scan(&task.ID, &task.Parent, &task.Child, &task.Text, &task.State, &task.Result, &task.Kind, &task.ResultTurnID, &task.Latest, &task.LatestTurnID, &latestAt, &task.LastTurn, &task.LastState, &task.LastText, &task.Stalled, &task.Wake, &createdAt, &settledAt)
	task.CreatedAt = time.UnixMilli(createdAt)
	if latestAt > 0 {
		task.LatestAt = time.UnixMilli(latestAt)
	}
	if settledAt > 0 {
		task.SettledAt = time.UnixMilli(settledAt)
	}
	return task, err
}

func clipTaskText(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit]
}

func (r *Registry) CreateTask(parent, child, text string, at time.Time) (Task, error) {
	if _, err := r.db.Exec(`INSERT INTO tasks(parent_session_id,child_session_id,task,created_at_ms) VALUES(?,?,?,?) ON CONFLICT(child_session_id) DO NOTHING`, parent, child, clipTaskText(text, taskTextLimit), at.UnixMilli()); err != nil {
		return Task{}, err
	}
	return scanTask(r.db.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE child_session_id=?`, child))
}

func (r *Registry) TaskForChild(child string) (Task, bool, error) {
	return foundTask(scanTask(r.read.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE child_session_id=?`, child)))
}

func (r *Registry) TaskByID(id int64) (Task, bool, error) {
	return foundTask(scanTask(r.read.QueryRow(`SELECT `+taskColumns+` FROM tasks WHERE id=?`, id)))
}

func foundTask(task Task, err error) (Task, bool, error) {
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, false, nil
	}
	return task, err == nil, err
}

// The first settler wins; everyone after it has only latest.
func (r *Registry) SettleTask(child string, settlement TaskSettlement, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE tasks SET state=?,result_kind=?,result=?,result_turn_id=?,stalled=0,settled_at_ms=? WHERE child_session_id=? AND state='running'`,
		settlement.State, settlement.Kind, clipTaskText(settlement.Result, taskResultLimit), settlement.TurnID, at.UnixMilli(), child)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

// RecordTaskLatest takes the turn the text came from, or "" for a report.
func (r *Registry) RecordTaskLatest(child, turnID, text string, at time.Time) error {
	_, err := r.db.Exec(`UPDATE tasks SET latest=?,latest_turn_id=CASE WHEN ?2='' THEN latest_turn_id ELSE ?2 END,latest_at_ms=? WHERE child_session_id=? AND state<>'running'`, clipTaskText(text, taskResultLimit), turnID, at.UnixMilli(), child)
	return err
}

// RecordTaskTurn keeps a finished turn that a goal holds open, the result left
// if the child is gone before the goal ends.
func (r *Registry) RecordTaskTurn(child, turnID, state, text string) error {
	_, err := r.db.Exec(`UPDATE tasks SET last_turn_id=?,last_state=?,last_text=? WHERE child_session_id=? AND state='running' AND last_turn_id<>?`, turnID, state, clipTaskText(text, taskResultLimit), child, turnID)
	return err
}

// MarkTaskStalled records whether the child's last turn can't be confirmed as
// over or running. Such a child stays open and holds no spawn slot.
func (r *Registry) MarkTaskStalled(child string, stalled bool) error {
	_, err := r.db.Exec(`UPDATE tasks SET stalled=?1 WHERE child_session_id=?2 AND state='running' AND stalled<>?1`, stalled, child)
	return err
}

// RunningTaskChildren maps each running task's child to whether it has left the catalog.
func (r *Registry) RunningTaskChildren() (map[string]bool, error) {
	type row struct {
		child string
		gone  bool
	}
	rows, err := queryRows(r.read, []row{}, func(scan rowScanner) (row, error) {
		var item row
		err := scan.Scan(&item.child, &item.gone)
		return item, err
	}, `SELECT t.child_session_id,(s.status='offline' AND c.session_id IS NULL)
		FROM tasks t JOIN sessions s ON s.id=t.child_session_id LEFT JOIN catalog_sessions c ON c.session_id=s.id
		WHERE t.state='running'`)
	if err != nil {
		return nil, err
	}
	children := make(map[string]bool, len(rows))
	for _, item := range rows {
		children[item.child] = item.gone
	}
	return children, nil
}

func (r *Registry) TaskChildren() (map[string]bool, error) {
	ids, err := queryRows(r.read, []string{}, func(scan rowScanner) (string, error) {
		var id string
		return id, scan.Scan(&id)
	}, `SELECT child_session_id FROM tasks`)
	if err != nil {
		return nil, err
	}
	children := make(map[string]bool, len(ids))
	for _, id := range ids {
		children[id] = true
	}
	return children, nil
}

// ArmTaskWake asks for a notice when a running task settles.
func (r *Registry) ArmTaskWake(id int64) error {
	_, err := r.db.Exec(`UPDATE tasks SET wake=1 WHERE id=? AND state='running'`, id)
	return err
}

func (r *Registry) TasksToWake() ([]Task, error) {
	return queryRows(r.read, []Task{}, scanTask, `SELECT `+taskColumns+` FROM tasks WHERE wake=1 AND state IN ('done','failed')`)
}

func (r *Registry) ClearTaskWake(id int64) error {
	_, err := r.db.Exec(`UPDATE tasks SET wake=0 WHERE id=? AND wake<>0`, id)
	return err
}

func cancelTaskTx(tx *sql.Tx, child string, at time.Time) error {
	_, err := tx.Exec(`UPDATE tasks SET state='cancelled',stalled=0,wake=0,settled_at_ms=? WHERE child_session_id=? AND state='running'`, at.UnixMilli(), child)
	return err
}

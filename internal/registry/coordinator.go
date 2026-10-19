package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

var ErrNoCoordinator = errors.New("no coordinator is set; use 'agenthail coordinator set'")

// CoordinatorSpec is how the coordinator starts again when its conversation
// cannot be resumed. The coordinator is whichever session its alias names, so
// the address follows rekeys and restarts.
type CoordinatorSpec struct {
	Name           string    `json:"name"`
	Surface        string    `json:"surface"`
	Cwd            string    `json:"cwd"`
	Model          string    `json:"model,omitempty"`
	PermissionMode string    `json:"permissionMode,omitempty"`
	Instructions   string    `json:"instructions,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

const coordinatorSchema = `
CREATE TABLE IF NOT EXISTS coordinator (
	singleton INTEGER PRIMARY KEY CHECK (singleton = 1),
	name TEXT NOT NULL,
	surface TEXT NOT NULL,
	cwd TEXT NOT NULL,
	model TEXT NOT NULL DEFAULT '',
	permission_mode TEXT NOT NULL DEFAULT '',
	instructions TEXT NOT NULL DEFAULT '',
	updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);`

// A restart holds this lock so concurrent senders in any Agenthail process
// start at most one conversation.
func (r *Registry) CoordinatorLockPath() string {
	return filepath.Join(filepath.Dir(r.path), "coordinator.lock")
}

func (r *Registry) Coordinator() (CoordinatorSpec, error) {
	var spec CoordinatorSpec
	var updated string
	err := r.read.QueryRow(`SELECT name,surface,cwd,model,permission_mode,instructions,updated_at FROM coordinator WHERE singleton=1`).Scan(&spec.Name, &spec.Surface, &spec.Cwd, &spec.Model, &spec.PermissionMode, &spec.Instructions, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return spec, ErrNoCoordinator
	}
	if err != nil {
		return spec, err
	}
	spec.UpdatedAt, _ = time.Parse(time.DateTime, updated)
	return spec, nil
}

func (r *Registry) SetCoordinator(spec CoordinatorSpec, sessionID string) error {
	if spec.Name == "" || spec.Surface == "" || spec.Cwd == "" {
		return fmt.Errorf("a coordinator needs a name, an agent and a working directory")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if sessionID != "" {
		if err := setAliasTx(tx, spec.Name, sessionID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO coordinator (singleton,name,surface,cwd,model,permission_mode,instructions,updated_at) VALUES (1,?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(singleton) DO UPDATE SET name=excluded.name,surface=excluded.surface,cwd=excluded.cwd,model=excluded.model,permission_mode=excluded.permission_mode,instructions=excluded.instructions,updated_at=excluded.updated_at`,
		spec.Name, spec.Surface, spec.Cwd, spec.Model, spec.PermissionMode, spec.Instructions); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) ClearCoordinator() error {
	res, err := r.db.Exec(`DELETE FROM coordinator WHERE singleton=1`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoCoordinator
	}
	return nil
}

func (r *Registry) CoordinatorSessionID(spec CoordinatorSpec) (string, error) {
	id, err := r.LookupAlias(spec.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

var ErrCoordinatorMoved = errors.New("the coordinator was moved to another conversation")

// HandOverCoordinator also moves messages still pending for the previous
// conversation, which can no longer take them.
func (r *Registry) HandOverCoordinator(name, previous, next string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, name).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != previous {
		return ErrCoordinatorMoved
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE name=? OR session_id=?`, name, next); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?)`, name, next); err != nil {
		return err
	}
	if previous != "" {
		if _, err := tx.Exec(`UPDATE message_queue SET session_id=?,updated_at=datetime('now') WHERE session_id=? AND status='pending'`, next, previous); err != nil {
			return err
		}
	}
	return tx.Commit()
}

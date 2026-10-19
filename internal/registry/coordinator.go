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
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`DELETE FROM coordinator WHERE singleton=1`)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoCoordinator
	}
	if _, err := tx.Exec(`DELETE FROM coordinator_replaced`); err != nil {
		return err
	}
	return tx.Commit()
}

// IsCoordinatorSession reports whether sessionID is the coordinator or a
// conversation a restart replaced, which senders such as a notification reply
// may still address.
func (r *Registry) IsCoordinatorSession(sessionID string) (bool, error) {
	spec, err := r.Coordinator()
	if errors.Is(err, ErrNoCoordinator) || sessionID == "" {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	current, err := r.CoordinatorSessionID(spec)
	if err != nil || current == sessionID {
		return err == nil, err
	}
	var replaced bool
	err = r.read.QueryRow(`SELECT EXISTS(SELECT 1 FROM coordinator_replaced WHERE session_id=?)`, sessionID).Scan(&replaced)
	return replaced, err
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
// conversation, which can no longer take them, and remembers it was replaced.
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
		if _, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?,updated_at=datetime('now') WHERE queue_id IN (SELECT id FROM message_queue WHERE session_id=? AND status='pending')`, next, previous); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE message_queue SET session_id=?,updated_at=datetime('now') WHERE session_id=? AND status='pending'`, next, previous); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO coordinator_replaced (session_id) VALUES (?)`, previous); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// MoveClaimedMessage returns a claimed queue item to the queue for sessionID,
// with its delivery intent, for a message whose target was replaced while it
// was in flight.
func (r *Registry) MoveClaimedMessage(id int64, sessionID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.Exec(`UPDATE message_queue SET session_id=?,status='pending',attempts=MAX(attempts-1,0),available_at_ms=0,inflight_at_ms=0,updated_at=datetime('now') WHERE id=? AND status='inflight'`, sessionID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n != 1 {
		return fmt.Errorf("queue item %d is not inflight", id)
	}
	if _, err := tx.Exec(`UPDATE delivery_intents SET target_session_id=?,updated_at=datetime('now') WHERE queue_id=?`, sessionID, id); err != nil {
		return err
	}
	return tx.Commit()
}

package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
)

var ErrNoCoordinator = errors.New("this project has no coordinator; use 'agenthail coordinator new' or 'agenthail coordinator set'")

// LeadAlias is not a session's name: "@lead" addresses the coordinator of the
// sender's project.
const LeadAlias = "lead"

var ErrReservedAlias = fmt.Errorf("@%s is reserved for the coordinator of the sender's project", LeadAlias)

// CoordinatorSpec is how a project's coordinator starts again when its
// conversation cannot be resumed. The coordinator is whichever session its
// alias names, so the address follows rekeys and restarts.
type CoordinatorSpec struct {
	ProjectID      string    `json:"projectId"`
	ProjectRoot    string    `json:"projectRoot"`
	Name           string    `json:"name"`
	Surface        string    `json:"surface"`
	Cwd            string    `json:"cwd"`
	Model          string    `json:"model,omitempty"`
	PermissionMode string    `json:"permissionMode,omitempty"`
	Instructions   string    `json:"instructions,omitempty"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

type CoordinatorNameTakenError struct {
	Name        string
	ProjectRoot string
}

func (e CoordinatorNameTakenError) Error() string {
	return fmt.Sprintf("@%s already coordinates %s", e.Name, e.ProjectRoot)
}

// A restart holds this lock so concurrent senders in any Agenthail process
// start at most one conversation for the project.
func (r *Registry) CoordinatorLockPath(projectID string) string {
	return filepath.Join(filepath.Dir(r.path), "coordinator-"+projectID+".lock")
}

const coordinatorColumns = `project_id,project_root,name,surface,cwd,model,permission_mode,instructions,updated_at`

func scanCoordinator(row interface{ Scan(...any) error }) (CoordinatorSpec, error) {
	var spec CoordinatorSpec
	var updated string
	if err := row.Scan(&spec.ProjectID, &spec.ProjectRoot, &spec.Name, &spec.Surface, &spec.Cwd, &spec.Model, &spec.PermissionMode, &spec.Instructions, &updated); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return spec, ErrNoCoordinator
		}
		return spec, err
	}
	spec.UpdatedAt, _ = time.Parse(time.DateTime, updated)
	return spec, nil
}

func (r *Registry) Coordinator(projectID string) (CoordinatorSpec, error) {
	return scanCoordinator(r.read.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators WHERE project_id=?`, projectID))
}

func (r *Registry) CoordinatorNamed(name string) (CoordinatorSpec, error) {
	return scanCoordinator(r.read.QueryRow(`SELECT `+coordinatorColumns+` FROM coordinators WHERE name=?`, name))
}

func (r *Registry) Coordinators() ([]CoordinatorSpec, error) {
	rows, err := r.read.Query(`SELECT ` + coordinatorColumns + ` FROM coordinators ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var specs []CoordinatorSpec
	for rows.Next() {
		spec, err := scanCoordinator(rows)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	return specs, rows.Err()
}

func (r *Registry) SetCoordinator(spec CoordinatorSpec, sessionID string) error {
	if spec.ProjectID == "" || spec.ProjectRoot == "" || spec.Name == "" || spec.Surface == "" || spec.Cwd == "" {
		return fmt.Errorf("a coordinator needs a project, a name, an agent and a working directory")
	}
	if strings.EqualFold(spec.Name, LeadAlias) {
		return ErrReservedAlias
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner, root string
	err = tx.QueryRow(`SELECT project_id,project_root FROM coordinators WHERE name=?`, spec.Name).Scan(&owner, &root)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && owner != spec.ProjectID {
		return CoordinatorNameTakenError{Name: spec.Name, ProjectRoot: root}
	}
	if sessionID != "" {
		if err := setAliasTx(tx, spec.Name, sessionID); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM coordinator_replaced WHERE session_id=?`, sessionID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(`INSERT INTO coordinators (`+coordinatorColumns+`) VALUES (?,?,?,?,?,?,?,?,datetime('now'))
		ON CONFLICT(project_id) DO UPDATE SET project_root=excluded.project_root,name=excluded.name,surface=excluded.surface,cwd=excluded.cwd,model=excluded.model,permission_mode=excluded.permission_mode,instructions=excluded.instructions,updated_at=excluded.updated_at`,
		spec.ProjectID, spec.ProjectRoot, spec.Name, spec.Surface, spec.Cwd, spec.Model, spec.PermissionMode, spec.Instructions); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Registry) ClearCoordinator(projectID string) error {
	res, err := r.db.Exec(`DELETE FROM coordinators WHERE project_id=?`, projectID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNoCoordinator
	}
	return nil
}

// CoordinatorOfSession returns the spec of the project sessionID coordinates,
// as its current conversation or one a restart replaced, which senders such as
// a notification reply may still address. Being a coordinator now outranks
// having been one.
func (r *Registry) CoordinatorOfSession(sessionID string) (CoordinatorSpec, bool, error) {
	if sessionID == "" {
		return CoordinatorSpec{}, false, nil
	}
	for _, owner := range []string{
		`SELECT ` + coordinatorColumns + ` FROM coordinators WHERE name IN (SELECT name FROM aliases WHERE session_id=?)`,
		`SELECT ` + coordinatorColumns + ` FROM coordinators WHERE project_id IN (SELECT project_id FROM coordinator_replaced WHERE session_id=?)`,
	} {
		spec, err := scanCoordinator(r.read.QueryRow(owner, sessionID))
		if errors.Is(err, ErrNoCoordinator) {
			continue
		}
		return spec, err == nil, err
	}
	return CoordinatorSpec{}, false, nil
}

// CoordinatorSessions maps each project id to the session its coordinator's
// name points at, leaving out coordinators that have not started.
func (r *Registry) CoordinatorSessions() (map[string]string, error) {
	rows, err := r.read.Query(`SELECT c.project_id,a.session_id FROM coordinators c JOIN aliases a ON a.name=c.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	sessions := map[string]string{}
	for rows.Next() {
		var projectID, sessionID string
		if err := rows.Scan(&projectID, &sessionID); err != nil {
			return nil, err
		}
		sessions[projectID] = sessionID
	}
	return sessions, rows.Err()
}

func (r *Registry) CoordinatorSessionID(spec CoordinatorSpec) (string, error) {
	id, err := r.LookupAlias(spec.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

var ErrCoordinatorMoved = errors.New("the coordinator was moved to another conversation")

// HandOverCoordinator points the coordinator's name at next. It also moves
// what the previous conversation can no longer use: messages still pending
// for it, its channel memberships and its relay routes, and remembers it was
// replaced.
func (r *Registry) HandOverCoordinator(spec CoordinatorSpec, previous, next string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT session_id FROM aliases WHERE name=?`, spec.Name).Scan(&owner)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != previous {
		return ErrCoordinatorMoved
	}
	if _, err := tx.Exec(`DELETE FROM aliases WHERE name=? OR session_id=?`, spec.Name, next); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM coordinator_replaced WHERE session_id=?`, next); err != nil {
		return err
	}
	if _, err := tx.Exec(`INSERT INTO aliases (name,session_id) VALUES (?,?)`, spec.Name, next); err != nil {
		return err
	}
	if previous != "" {
		for _, statement := range append([]string{
			`UPDATE delivery_intents SET target_session_id=?2,updated_at=datetime('now') WHERE queue_id IN (SELECT id FROM message_queue WHERE session_id=?1 AND status='pending')`,
			`UPDATE message_queue SET session_id=?2,updated_at=datetime('now') WHERE session_id=?1 AND status='pending'`,
			`INSERT OR IGNORE INTO channel_members (channel_id,session_id) SELECT channel_id,?2 FROM channel_members WHERE session_id=?1`,
			`DELETE FROM channel_members WHERE session_id=?1`,
		}, routeMoves...) {
			if _, err := tx.Exec(statement, previous, next); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO coordinator_replaced (session_id,project_id) VALUES (?,?)`, previous, spec.ProjectID); err != nil {
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

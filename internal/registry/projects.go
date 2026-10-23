package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ProjectRecord is a folder added as a project on this host, or the archive
// state of a repository's implicit project (Added false).
type ProjectRecord struct {
	ID            string
	WorkspaceRoot string
	Title         string
	Added         bool
	Key           string
	Repository    []byte
	CreatedAt     time.Time
	ArchivedAt    time.Time
}

type ArchiveState struct {
	Sessions map[string]time.Time
	Projects map[string]time.Time
}

// WorkspaceTakenError refuses a second project for a folder.
type WorkspaceTakenError struct {
	WorkspaceRoot string
	ProjectID     string
}

func (e *WorkspaceTakenError) Error() string {
	return fmt.Sprintf("Workspace %s already belongs to project %s.", e.WorkspaceRoot, e.ProjectID)
}

// AddProject records an added project. An implicit project's archive row for
// the same folder gives way to it and passes on its archive state.
func (r *Registry) AddProject(project ProjectRecord) error {
	if project.ID == "" || project.WorkspaceRoot == "" || project.Title == "" {
		return fmt.Errorf("project id, workspace root and title are required")
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingID string
	var added int
	var archivedAt int64
	err = tx.QueryRow(`SELECT id,added,archived_at_ms FROM projects WHERE workspace_root=?`, project.WorkspaceRoot).Scan(&existingID, &added, &archivedAt)
	switch {
	case err == nil && added == 1:
		return &WorkspaceTakenError{WorkspaceRoot: project.WorkspaceRoot, ProjectID: existingID}
	case err == nil:
		if _, err := tx.Exec(`DELETE FROM projects WHERE id=?`, existingID); err != nil {
			return err
		}
	case !errors.Is(err, sql.ErrNoRows):
		return err
	}
	if _, err := tx.Exec(`INSERT INTO projects(id,workspace_root,title,added,project_key,repository,created_at_ms,archived_at_ms) VALUES(?,?,?,1,?,?,?,?)`,
		project.ID, project.WorkspaceRoot, project.Title, project.Key, append([]byte{}, project.Repository...), project.CreatedAt.UnixMilli(), archivedAt); err != nil {
		return err
	}
	return tx.Commit()
}

// RemoveProject deletes an added project. Its sessions are untouched.
func (r *Registry) RemoveProject(id string) error {
	result, err := r.db.Exec(`DELETE FROM projects WHERE id=? AND added=1`, id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetProjectTitle names an added project; an id of no added project is
// sql.ErrNoRows.
func (r *Registry) SetProjectTitle(id, title string) error {
	result, err := r.db.Exec(`UPDATE projects SET title=? WHERE id=? AND added=1`, title, id)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Registry) SetProjectRepository(id, key string, repository []byte) error {
	_, err := r.db.Exec(`UPDATE projects SET project_key=?,repository=? WHERE id=? AND added=1`, key, append([]byte{}, repository...), id)
	return err
}

func (r *Registry) ListProjects() ([]ProjectRecord, error) {
	rows, err := r.read.Query(`SELECT id,workspace_root,title,added,project_key,repository,created_at_ms,archived_at_ms FROM projects ORDER BY title,workspace_root`)
	if err != nil {
		return nil, err
	}
	return appendRows([]ProjectRecord{}, rows, func(row rowScanner) (ProjectRecord, error) {
		var project ProjectRecord
		var added int
		var createdAt, archivedAt int64
		err := row.Scan(&project.ID, &project.WorkspaceRoot, &project.Title, &added, &project.Key, &project.Repository, &createdAt, &archivedAt)
		project.Added = added == 1
		project.CreatedAt = time.UnixMilli(createdAt).UTC()
		if archivedAt > 0 {
			project.ArchivedAt = time.UnixMilli(archivedAt).UTC()
		}
		return project, err
	})
}

// ArchiveProject archives an added project, or writes the archive row of an
// implicit one.
func (r *Registry) ArchiveProject(project ProjectRecord, at time.Time) error {
	if project.ID == "" || project.WorkspaceRoot == "" {
		return fmt.Errorf("project id and workspace root are required")
	}
	_, err := r.db.Exec(`INSERT INTO projects(id,workspace_root,title,added,archived_at_ms) VALUES(?,?,?,0,?)
		ON CONFLICT(id) DO UPDATE SET archived_at_ms=excluded.archived_at_ms`, project.ID, project.WorkspaceRoot, project.Title, at.UnixMilli())
	return err
}

func (r *Registry) UnarchiveProject(id string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := unarchiveProjectTx(tx, id); err != nil {
		return err
	}
	return tx.Commit()
}

// An implicit project's row exists only to hold its archive state, so
// unarchiving removes it; an added project's row stays.
func unarchiveProjectTx(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`UPDATE projects SET archived_at_ms=0 WHERE id=?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM projects WHERE id=? AND added=0`, id)
	return err
}

func (r *Registry) ArchiveSession(sessionID string, at time.Time) error {
	result, err := r.db.Exec(`INSERT INTO session_archive(session_id,archived_at_ms) SELECT id,? FROM sessions WHERE id=?
		ON CONFLICT(session_id) DO UPDATE SET archived_at_ms=excluded.archived_at_ms`, at.UnixMilli(), sessionID)
	if err != nil {
		return err
	}
	if changed, err := result.RowsAffected(); err != nil {
		return err
	} else if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (r *Registry) UnarchiveSession(sessionID string) error {
	_, err := r.db.Exec(`DELETE FROM session_archive WHERE session_id=?`, sessionID)
	return err
}

func (r *Registry) ClearArchiveForActivity(sessionID, projectID string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM session_archive WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	if projectID != "" {
		if err := unarchiveProjectTx(tx, projectID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Registry) ArchiveState() (ArchiveState, error) {
	state := ArchiveState{Sessions: map[string]time.Time{}, Projects: map[string]time.Time{}}
	rows, err := r.read.Query(`SELECT session_id,archived_at_ms FROM session_archive`)
	if err != nil {
		return state, err
	}
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			rows.Close()
			return state, err
		}
		state.Sessions[id] = time.UnixMilli(at).UTC()
	}
	if err := rows.Close(); err != nil {
		return state, err
	}
	rows, err = r.read.Query(`SELECT id,archived_at_ms FROM projects WHERE archived_at_ms>0`)
	if err != nil {
		return state, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var at int64
		if err := rows.Scan(&id, &at); err != nil {
			return state, err
		}
		state.Projects[id] = time.UnixMilli(at).UTC()
	}
	return state, rows.Err()
}

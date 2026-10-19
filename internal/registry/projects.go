package registry

import (
	"database/sql"
	"fmt"
	"time"
)

type ProjectRecord struct {
	ID          string
	Kind        string
	RootPath    string
	DisplayName string
	Registered  bool
	ArchivedAt  time.Time
}

type ArchiveState struct {
	Sessions map[string]time.Time
	Projects map[string]time.Time
}

func (r *Registry) RegisterFolderProject(id, rootPath, displayName string) error {
	if id == "" || rootPath == "" {
		return fmt.Errorf("project id and root path are required")
	}
	_, err := r.db.Exec(`INSERT INTO projects(id,kind,root_path,display_name,registered) VALUES(?,'folder',?,?,1)
		ON CONFLICT(id) DO UPDATE SET kind='folder',root_path=excluded.root_path,display_name=excluded.display_name,registered=1`, id, rootPath, displayName)
	return err
}

func (r *Registry) ListProjects() ([]ProjectRecord, error) {
	rows, err := r.read.Query(`SELECT id,kind,root_path,display_name,registered,archived_at_ms FROM projects ORDER BY display_name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	projects := []ProjectRecord{}
	for rows.Next() {
		var project ProjectRecord
		var registered int
		var archivedAt int64
		if err := rows.Scan(&project.ID, &project.Kind, &project.RootPath, &project.DisplayName, &registered, &archivedAt); err != nil {
			return nil, err
		}
		project.Registered = registered == 1
		if archivedAt > 0 {
			project.ArchivedAt = time.UnixMilli(archivedAt).UTC()
		}
		projects = append(projects, project)
	}
	return projects, rows.Err()
}

func (r *Registry) ArchiveProject(project ProjectRecord, at time.Time) error {
	if project.ID == "" || project.Kind == "" {
		return fmt.Errorf("project id and kind are required")
	}
	_, err := r.db.Exec(`INSERT INTO projects(id,kind,root_path,display_name,archived_at_ms) VALUES(?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET archived_at_ms=excluded.archived_at_ms`, project.ID, project.Kind, project.RootPath, project.DisplayName, at.UnixMilli())
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

// A git project row exists only to hold its archive state, so unarchiving
// removes it; a registered folder row stays.
func unarchiveProjectTx(tx *sql.Tx, id string) error {
	if _, err := tx.Exec(`UPDATE projects SET archived_at_ms=0 WHERE id=?`, id); err != nil {
		return err
	}
	_, err := tx.Exec(`DELETE FROM projects WHERE id=? AND registered=0`, id)
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

package registry

import (
	"database/sql"
	"errors"
	"time"
)

// LedgerProjectKey names the ledger of an added project that has no
// repository key. The project's id is unique on every host and stays when
// its folder moves.
func LedgerProjectKey(projectID string) string { return "project:" + projectID }

// A ledger exists once anything is kept under its key: an event, a session's
// grant, or an owner.
func holdsLedger(q interface {
	QueryRow(string, ...any) *sql.Row
}, key string) (bool, error) {
	var one int
	err := q.QueryRow(`SELECT 1 FROM ledger_events WHERE project_key=?1
		UNION ALL SELECT 1 FROM ledger_grants WHERE project_key=?1
		UNION ALL SELECT 1 FROM ledger_state WHERE project_key=?1 AND owner_host_id!='' LIMIT 1`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r *Registry) HoldsLedger(key string) (bool, error) { return holdsLedger(r.read, key) }

// RemovedProjectLedger names the project whose ledger stayed behind when the
// folder at workspaceRoot was removed, so adding the folder again takes it
// back.
func (r *Registry) RemovedProjectLedger(workspaceRoot string) (string, bool, error) {
	var id string
	err := r.read.QueryRow(`SELECT project_id FROM removed_project_ledgers WHERE workspace_root=?`, workspaceRoot).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return id, err == nil, err
}

func keepRemovedProjectLedger(tx *sql.Tx, id string, at time.Time) error {
	var root string
	if err := tx.QueryRow(`SELECT workspace_root FROM projects WHERE id=? AND added=1`, id).Scan(&root); err != nil {
		return err
	}
	held, err := holdsLedger(tx, LedgerProjectKey(id))
	if err != nil || !held {
		return err
	}
	_, err = tx.Exec(`INSERT INTO removed_project_ledgers(workspace_root,project_id,removed_at_ms) VALUES(?,?,?)
		ON CONFLICT(workspace_root) DO UPDATE SET project_id=excluded.project_id, removed_at_ms=excluded.removed_at_ms`, root, id, at.UnixMilli())
	return err
}

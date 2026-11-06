package registry

import (
	"encoding/json"
	"time"
)

const (
	WorkCreating = "creating"
	WorkActive   = "active"
	WorkSettled  = "settled"

	WorkSettledTaskReported = "task_reported"
	WorkSettledArchived     = "archived"
	WorkSettledPerson       = "person"
	WorkSettledOrphaned     = "orphaned"

	WorkCleanupPending = "pending"
	WorkCleanupRemoved = "removed"
	WorkCleanupKept    = "kept"

	WorkKeptRunning = "running"
)

// WorkRecord is a worktree Agenthail made for a session. Its row is written
// before the worktree, so a crash between the two leaves a row to reconcile,
// never a worktree nobody knows of.
type WorkRecord struct {
	WorktreePath  string
	SessionID     string
	RepoRoot      string
	Branch        string
	BaseRef       string
	BaseCommit    string
	State         string
	SettledReason string
	Cleanup       string
	CleanupReason string
	KeptPaths     []string
	CreatedAt     time.Time
	SettledAt     time.Time
}

func (r *Registry) BeginWork(record WorkRecord, at time.Time) error {
	_, err := r.db.Exec(`INSERT INTO work_records(worktree_path,repo_root,branch,base_ref,base_commit,state,created_at_ms) VALUES(?,?,?,?,?,?,?)`,
		record.WorktreePath, record.RepoRoot, record.Branch, record.BaseRef, record.BaseCommit, WorkCreating, at.UnixMilli())
	return err
}

func (r *Registry) DropWork(worktreePath string) error {
	_, err := r.db.Exec(`DELETE FROM work_records WHERE worktree_path=?`, worktreePath)
	return err
}

// SettleWork marks a session's active worktree done and due for cleanup; false
// when the session has none or it was already settled.
func (r *Registry) SettleWork(sessionID, reason string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE work_records SET state=?,settled_reason=?,settled_at_ms=?,cleanup=?,cleanup_reason='',kept_paths='' WHERE session_id=? AND state=?`,
		WorkSettled, reason, at.UnixMilli(), WorkCleanupPending, sessionID, WorkActive)
	if err != nil {
		return false, err
	}
	settled, err := result.RowsAffected()
	return settled == 1, err
}

// SettleOrphanWork marks work whose create never recorded a session due for
// cleanup; false when it is no longer being created.
func (r *Registry) SettleOrphanWork(worktreePath string, at time.Time) (bool, error) {
	result, err := r.db.Exec(`UPDATE work_records SET state=?,settled_reason=?,settled_at_ms=?,cleanup=? WHERE worktree_path=? AND state=?`,
		WorkSettled, WorkSettledOrphaned, at.UnixMilli(), WorkCleanupPending, worktreePath, WorkCreating)
	if err != nil {
		return false, err
	}
	settled, err := result.RowsAffected()
	return settled == 1, err
}

// ActivateWork ties work still being created to its session; false when it is
// no longer being created.
func (r *Registry) ActivateWork(worktreePath, sessionID string) (bool, error) {
	result, err := r.db.Exec(`UPDATE work_records SET session_id=?,state=? WHERE worktree_path=? AND state=?`, sessionID, WorkActive, worktreePath, WorkCreating)
	if err != nil {
		return false, err
	}
	activated, err := result.RowsAffected()
	return activated == 1, err
}

func (r *Registry) SetWorkCleanup(worktreePath, cleanup, reason string, keptPaths []string) error {
	kept := ""
	if len(keptPaths) > 0 {
		encoded, err := json.Marshal(keptPaths)
		if err != nil {
			return err
		}
		kept = string(encoded)
	}
	_, err := r.db.Exec(`UPDATE work_records SET cleanup=?,cleanup_reason=?,kept_paths=? WHERE worktree_path=?`, cleanup, reason, kept, worktreePath)
	return err
}

func (r *Registry) WorkAt(worktreePath string) (WorkRecord, bool, error) {
	records, err := r.workRecords(`WHERE worktree_path=?`, worktreePath)
	if err != nil || len(records) == 0 {
		return WorkRecord{}, false, err
	}
	return records[0], true, nil
}

func (r *Registry) WorkForSession(sessionID string) (WorkRecord, bool, error) {
	records, err := r.workRecords(`WHERE session_id=?`, sessionID)
	if err != nil || len(records) == 0 {
		return WorkRecord{}, false, err
	}
	return records[0], true, nil
}

// WorkToClean is settled work whose cleanup has not run, or was put off
// because the session was still running a turn.
func (r *Registry) WorkToClean() ([]WorkRecord, error) {
	return r.workRecords(`WHERE state=? AND (cleanup=? OR (cleanup=? AND cleanup_reason=?))`, WorkSettled, WorkCleanupPending, WorkCleanupKept, WorkKeptRunning)
}

// StaleCreatingWork is work begun before the cutoff whose session was never
// recorded: a create that crashed or failed between its row and its start.
func (r *Registry) StaleCreatingWork(before time.Time) ([]WorkRecord, error) {
	return r.workRecords(`WHERE state=? AND created_at_ms<?`, WorkCreating, before.UnixMilli())
}

func (r *Registry) SessionWork() (map[string]WorkRecord, error) {
	records, err := r.workRecords(`WHERE session_id<>''`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]WorkRecord, len(records))
	for _, record := range records {
		out[record.SessionID] = record
	}
	return out, nil
}

func (r *Registry) workRecords(where string, args ...any) ([]WorkRecord, error) {
	rows, err := r.read.Query(`SELECT worktree_path,session_id,repo_root,branch,base_ref,base_commit,state,settled_reason,cleanup,cleanup_reason,kept_paths,created_at_ms,settled_at_ms FROM work_records `+where+` ORDER BY created_at_ms`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WorkRecord
	for rows.Next() {
		var record WorkRecord
		var created, settled int64
		var kept string
		if err := rows.Scan(&record.WorktreePath, &record.SessionID, &record.RepoRoot, &record.Branch, &record.BaseRef, &record.BaseCommit, &record.State, &record.SettledReason, &record.Cleanup, &record.CleanupReason, &kept, &created, &settled); err != nil {
			return nil, err
		}
		if kept != "" {
			if err := json.Unmarshal([]byte(kept), &record.KeptPaths); err != nil {
				return nil, err
			}
		}
		record.CreatedAt = time.UnixMilli(created).UTC()
		if settled > 0 {
			record.SettledAt = time.UnixMilli(settled).UTC()
		}
		out = append(out, record)
	}
	return out, rows.Err()
}

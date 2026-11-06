package registry

import (
	"database/sql"
	"errors"
	"time"
)

// ProjectMove records that the repository project From is now To, because
// its remote changed. An empty To means From names a project of its own
// again. Of two records for one From the later wins, the origin host
// breaking a tie.
type ProjectMove struct {
	From       string    `json:"from"`
	To         string    `json:"to"`
	ChangedAt  time.Time `json:"changedAt"`
	OriginHost string    `json:"originHost"`
}

func (m ProjectMove) Shared() bool {
	return SharedProjectID(m.From) && (m.To == "" || SharedProjectID(m.To))
}

func (m ProjectMove) newerThan(other ProjectMove) bool {
	if !m.ChangedAt.Equal(other.ChangedAt) {
		return m.ChangedAt.After(other.ChangedAt)
	}
	return m.OriginHost > other.OriginHost
}

type ProjectMoveRefusal struct {
	From   string    `json:"from"`
	To     string    `json:"to"`
	Reason string    `json:"reason"`
	At     time.Time `json:"at"`
}

func (r *Registry) ProjectMoves() ([]ProjectMove, error) {
	rows, err := r.read.Query(`SELECT from_key,to_key,changed_at_ms,origin_host FROM project_moves ORDER BY from_key`)
	if err != nil {
		return nil, err
	}
	return appendRows([]ProjectMove{}, rows, func(row rowScanner) (ProjectMove, error) {
		var move ProjectMove
		var at int64
		err := row.Scan(&move.From, &move.To, &at, &move.OriginHost)
		move.ChangedAt = time.UnixMilli(at)
		return move, err
	})
}

func (r *Registry) ProjectMoveTargets() (map[string]string, error) {
	moves, err := r.ProjectMoves()
	if err != nil {
		return nil, err
	}
	targets := map[string]string{}
	for _, move := range moves {
		if move.To != "" {
			targets[move.From] = move.To
		}
	}
	return targets, nil
}

func (r *Registry) ProjectMoveRefusals() ([]ProjectMoveRefusal, error) {
	return r.projectMoveRefusals(`SELECT from_key,to_key,reason,at_ms FROM project_move_refusals ORDER BY from_key`)
}

func (r *Registry) ProjectMoveRefusalsOf(key string) ([]ProjectMoveRefusal, error) {
	return r.projectMoveRefusals(`SELECT from_key,to_key,reason,at_ms FROM project_move_refusals WHERE from_key=?1 OR to_key=?1 ORDER BY from_key`, key)
}

func (r *Registry) projectMoveRefusals(query string, args ...any) ([]ProjectMoveRefusal, error) {
	rows, err := r.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return appendRows([]ProjectMoveRefusal{}, rows, func(row rowScanner) (ProjectMoveRefusal, error) {
		var refusal ProjectMoveRefusal
		var at int64
		err := row.Scan(&refusal.From, &refusal.To, &refusal.Reason, &at)
		refusal.At = time.UnixMilli(at)
		return refusal, err
	})
}

// A folder whose remote is gone keeps the last key it had.
func (r *Registry) ObserveRepositoryKey(root, key string) (ProjectMoveOutcome, error) {
	if root == "" || key == "" {
		return ProjectMoveOutcome{}, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return ProjectMoveOutcome{}, err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRow(`SELECT project_key FROM repository_keys WHERE workspace_root=?`, root).Scan(&previous)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ProjectMoveOutcome{}, err
	}
	var outcome ProjectMoveOutcome
	switch previous {
	case key:
		if _, err := tx.Exec(`DELETE FROM project_move_refusals WHERE from_key=?`, key); err != nil {
			return outcome, err
		}
		return outcome, tx.Commit()
	case "":
	default:
		identity, err := hostIdentityTx(tx)
		if err != nil {
			return outcome, err
		}
		move := ProjectMove{From: previous, To: key, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity}
		if outcome, err = applyProjectMoveTx(tx, move); err != nil {
			return outcome, err
		}
		if outcome.Refused != nil {
			return outcome, tx.Commit()
		}
	}
	if _, err := tx.Exec(`INSERT INTO repository_keys(workspace_root,project_key) VALUES(?,?)
		ON CONFLICT(workspace_root) DO UPDATE SET project_key=excluded.project_key`, root, key); err != nil {
		return outcome, err
	}
	return outcome, tx.Commit()
}

func (r *Registry) AdoptProjectMoves(moves []ProjectMove) ([]ProjectMoveOutcome, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var outcomes []ProjectMoveOutcome
	for _, move := range moves {
		if !move.Shared() || move.OriginHost == "" || move.From == move.To {
			continue
		}
		current, found, err := projectMoveTx(tx, move.From)
		if err != nil {
			return nil, err
		}
		if found && !move.newerThan(current) {
			continue
		}
		outcome, err := applyProjectMoveTx(tx, move)
		if err != nil {
			return nil, err
		}
		if outcome.Moved || outcome.Refused != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, tx.Commit()
}

func projectMoveTx(tx *sql.Tx, from string) (ProjectMove, bool, error) {
	move := ProjectMove{From: from}
	var at int64
	err := tx.QueryRow(`SELECT to_key,changed_at_ms,origin_host FROM project_moves WHERE from_key=?`, from).Scan(&move.To, &at, &move.OriginHost)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectMove{}, false, nil
	}
	move.ChangedAt = time.UnixMilli(at)
	return move, err == nil, err
}

func hostIdentityTx(tx *sql.Tx) (string, error) {
	var id string
	err := tx.QueryRow(`SELECT host_id FROM host_identity WHERE id=1`).Scan(&id)
	return id, err
}

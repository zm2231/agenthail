package registry

import (
	"database/sql"
	"slices"
	"time"
)

func waitForArrivingLeadTx(tx *sql.Tx, move ProjectMove) (bool, error) {
	var queued bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM project_moves_waiting WHERE from_key=?)`, move.From).Scan(&queued); err != nil {
		return false, err
	}
	if !queued {
		moves, err := arrivingMovesTx(tx)
		if err != nil {
			return false, err
		}
		queued = slices.ContainsFunc(moves, func(arriving arrivingMove) bool {
			lead := arriving.Coordinator
			return lead != nil && (lead.ProjectID == move.From || lead.ProjectID == move.To)
		})
	}
	if !queued {
		return false, nil
	}
	_, err := tx.Exec(`INSERT INTO project_moves_waiting(from_key,to_key,changed_at_ms,origin_host) VALUES(?1,?2,?3,?4)
		ON CONFLICT(from_key) DO UPDATE SET to_key=excluded.to_key,changed_at_ms=excluded.changed_at_ms,origin_host=excluded.origin_host,workspace_root=''
		WHERE excluded.changed_at_ms>project_moves_waiting.changed_at_ms
			OR (excluded.changed_at_ms=project_moves_waiting.changed_at_ms AND excluded.origin_host>=project_moves_waiting.origin_host)`,
		move.From, move.To, move.ChangedAt.UnixMilli(), move.OriginHost)
	return true, err
}

func (r *Registry) ReleaseWaitingProjectMoves() ([]ProjectMoveOutcome, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	type waiting struct {
		move ProjectMove
		root string
	}
	parked, err := queryRows(tx, []waiting{}, func(row rowScanner) (waiting, error) {
		var w waiting
		var at int64
		err := row.Scan(&w.move.From, &w.move.To, &at, &w.move.OriginHost, &w.root)
		w.move.ChangedAt = time.UnixMilli(at).UTC()
		return w, err
	}, `SELECT from_key,to_key,changed_at_ms,origin_host,workspace_root FROM project_moves_waiting ORDER BY changed_at_ms,from_key`)
	if err != nil || len(parked) == 0 {
		return nil, err
	}
	var outcomes []ProjectMoveOutcome
	for _, w := range parked {
		if _, err := tx.Exec(`DELETE FROM project_moves_waiting WHERE from_key=?`, w.move.From); err != nil {
			return nil, err
		}
		var outcome ProjectMoveOutcome
		if w.root != "" {
			outcome, err = r.observeRepositoryKeyTx(tx, w.root, w.move.To)
		} else {
			outcome, err = applyProjectMoveTx(tx, w.move)
		}
		if err != nil {
			return nil, err
		}
		if outcome.Moved || outcome.Refused != nil {
			outcomes = append(outcomes, outcome)
		}
	}
	return outcomes, tx.Commit()
}

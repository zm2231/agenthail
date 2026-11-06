package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

type ProjectMoveOutcome struct {
	Move       ProjectMove
	Moved      bool
	Refused    *ProjectMoveRefusal
	NewRefusal bool
}

func applyProjectMoveTx(tx *sql.Tx, move ProjectMove) (ProjectMoveOutcome, error) {
	outcome := ProjectMoveOutcome{Move: move}
	if move.To == "" {
		outcome.Moved = true
		return outcome, putProjectMoveTx(tx, move)
	}
	current, found, err := projectMoveTx(tx, move.From)
	if err != nil {
		return outcome, err
	}
	held, err := projectHoldings(tx, move.From)
	if err != nil {
		return outcome, err
	}
	if found && current.To == move.To && !held.any() {
		return outcome, nil
	}
	target, err := projectHoldings(tx, move.To)
	if err != nil {
		return outcome, err
	}
	if reason := held.clash(target, move); reason != "" {
		return refuseProjectMoveTx(tx, move, reason)
	}
	if err := moveProjectRowsTx(tx, move, held); err != nil {
		return outcome, err
	}
	if err := moveProjectLinksTx(tx, move); err != nil {
		return outcome, err
	}
	for _, statement := range []string{
		`UPDATE project_moves SET to_key='', changed_at_ms=?3, origin_host=?4 WHERE from_key=?2`,
		`UPDATE project_moves SET to_key=?2, changed_at_ms=?3, origin_host=?4 WHERE to_key=?1 AND from_key!=?2`,
		`DELETE FROM project_move_refusals WHERE from_key=?1`,
	} {
		if _, err := tx.Exec(statement, move.From, move.To, move.ChangedAt.UnixMilli(), move.OriginHost); err != nil {
			return outcome, err
		}
	}
	outcome.Moved = true
	return outcome, putProjectMoveTx(tx, move)
}

type projectHolding struct{ lead, ledger, ownerless bool }

func (h projectHolding) any() bool { return h.lead || h.ledger || h.ownerless }

func (h projectHolding) clash(target projectHolding, move ProjectMove) string {
	switch {
	case h.ledger && target.ledger:
		return fmt.Sprintf("%s and %s each keep a ledger of their own, and two ledgers cannot be merged", move.From, move.To)
	case h.lead && target.lead:
		return fmt.Sprintf("%s and %s each have a lead of their own", move.From, move.To)
	}
	return ""
}

func projectHoldings(tx *sql.Tx, id string) (projectHolding, error) {
	var h projectHolding
	var err error
	if h.ledger, err = holdsLedger(tx, id); err != nil {
		return h, err
	}
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM coordinators WHERE project_id=? AND host_id=''`, id).Scan(&n); err != nil {
		return h, err
	}
	h.lead = n > 0
	if err := tx.QueryRow(`SELECT (SELECT COUNT(*) FROM ledger_state WHERE project_key=?1)
		+ (SELECT COUNT(*) FROM coordinators WHERE project_id=?1) + (SELECT COUNT(*) FROM coordinator_history WHERE project_id=?1)
		+ (SELECT COUNT(*) FROM project_links WHERE linked=1 AND (joined=?1 OR survivor=?1)) + (SELECT COUNT(*) FROM projects WHERE id=?1 AND added=0)`, id).Scan(&n); err != nil {
		return h, err
	}
	h.ownerless = n > 0
	return h, nil
}

func moveProjectRowsTx(tx *sql.Tx, move ProjectMove, held projectHolding) error {
	state := `DELETE FROM ledger_state WHERE project_key=?1`
	if held.ledger {
		state = `DELETE FROM ledger_state WHERE project_key=?2`
	}
	for _, statement := range []string{
		state,
		`UPDATE ledger_state SET project_key=?2 WHERE project_key=?1`,
		`UPDATE ledger_events SET project_key=?2 WHERE project_key=?1`,
		`UPDATE ledger_grants SET project_key=?2 WHERE project_key=?1`,
		`UPDATE coordinators SET project_id=?2 WHERE project_id=?1 AND host_id=''`,
		`UPDATE OR REPLACE coordinators SET project_id=?2 WHERE project_id=?1 AND host_id!=''`,
		`UPDATE coordinator_history SET project_id=?2 WHERE project_id=?1`,
		`UPDATE coordinator_replaced SET project_id=?2 WHERE project_id=?1`,
		`UPDATE OR IGNORE projects SET id=?2 WHERE id=?1 AND added=0`,
		`DELETE FROM projects WHERE id=?1 AND added=0`,
	} {
		if _, err := tx.Exec(statement, move.From, move.To); err != nil {
			return err
		}
	}
	return nil
}

func moveProjectLinksTx(tx *sql.Tx, move ProjectMove) error {
	rows, err := tx.Query(`SELECT joined,survivor,linked,changed_at_ms,origin_host FROM project_links WHERE linked=1 AND (joined=?1 OR survivor=?1)`, move.From)
	if err != nil {
		return err
	}
	links, err := appendRows([]ProjectLink{}, rows, func(row rowScanner) (ProjectLink, error) {
		var link ProjectLink
		var at int64
		err := row.Scan(&link.Joined, &link.Survivor, &link.Linked, &at, &link.OriginHost)
		link.ChangedAt = time.UnixMilli(at)
		return link, err
	})
	if err != nil {
		return err
	}
	for _, link := range links {
		at := move.ChangedAt
		if !at.After(link.ChangedAt) {
			at = link.ChangedAt.Add(time.Millisecond)
		}
		rewritten := ProjectLink{Joined: link.Joined, Survivor: link.Survivor, Linked: link.Linked, ChangedAt: at, OriginHost: move.OriginHost}
		if link.Survivor == move.From {
			rewritten.Survivor = move.To
		}
		if link.Joined == move.From {
			if err := putProjectLinkTx(tx, ProjectLink{Joined: move.From, Survivor: link.Survivor, ChangedAt: at, OriginHost: move.OriginHost}); err != nil {
				return err
			}
			if _, taken, err := projectLinkTx(tx, move.To); err != nil || taken || !link.Linked || rewritten.Survivor == move.To {
				if err != nil {
					return err
				}
				continue
			}
			rewritten.Joined = move.To
		}
		if rewritten.Joined == rewritten.Survivor {
			rewritten.Linked = false
		}
		if err := putProjectLinkTx(tx, rewritten); err != nil {
			return err
		}
	}
	return nil
}

func refuseProjectMoveTx(tx *sql.Tx, move ProjectMove, reason string) (ProjectMoveOutcome, error) {
	refusal := ProjectMoveRefusal{From: move.From, To: move.To, Reason: reason, At: move.ChangedAt}
	outcome := ProjectMoveOutcome{Move: move, Refused: &refusal}
	var same int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM project_move_refusals WHERE from_key=? AND to_key=?`, move.From, move.To).Scan(&same); err != nil {
		return outcome, err
	}
	outcome.NewRefusal = same == 0
	_, err := tx.Exec(`INSERT INTO project_move_refusals(from_key,to_key,reason,at_ms) VALUES(?,?,?,?)
		ON CONFLICT(from_key) DO UPDATE SET to_key=excluded.to_key, reason=excluded.reason, at_ms=excluded.at_ms`, move.From, move.To, reason, move.ChangedAt.UnixMilli())
	return outcome, err
}

func putProjectMoveTx(tx *sql.Tx, move ProjectMove) error {
	if _, err := tx.Exec(`INSERT INTO project_moves(from_key,to_key,changed_at_ms,origin_host) VALUES(?,?,?,?)
		ON CONFLICT(from_key) DO UPDATE SET to_key=excluded.to_key, changed_at_ms=excluded.changed_at_ms, origin_host=excluded.origin_host`,
		move.From, move.To, move.ChangedAt.UnixMilli(), move.OriginHost); err != nil {
		return err
	}
	payload, err := json.Marshal(move)
	if err != nil {
		return err
	}
	_, _, err = appendCatalogEventTx(tx, CatalogEvent{DedupeKey: fmt.Sprintf("project.moved:%s:%d:%s", move.From, move.ChangedAt.UnixMilli(), move.OriginHost), Type: "project.moved", EntityID: move.From, Payload: payload})
	return err
}

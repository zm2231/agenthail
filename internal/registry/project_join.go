package registry

import (
	"database/sql"
	"errors"
	"time"
)

type ProjectJoinRefusal struct{ Reason string }

func (e ProjectJoinRefusal) Error() string { return e.Reason }

// JoinProjectKey makes the added project id a checkout of the project key
// names. What the project kept under its own id (its lead and their
// history, settings, links, tile and ledger) moves to the key.
func (r *Registry) JoinProjectKey(id, key string) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var current string
	if err := tx.QueryRow(`SELECT project_key FROM projects WHERE id=? AND added=1`, id).Scan(&current); err != nil {
		return err
	}
	switch current {
	case key:
		return nil
	case "":
	default:
		return ProjectJoinRefusal{Reason: "the folder is already a checkout of " + current}
	}
	own := LedgerProjectKey(id)
	var owner string
	if err := tx.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, own).Scan(&owner); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != "" {
		return ProjectJoinRefusal{Reason: "its ledger is kept on another host"}
	}
	ownLedger, err := holdsLedger(tx, own)
	if err != nil {
		return err
	}
	keyLedger, err := holdsLedger(tx, key)
	if err != nil {
		return err
	}
	if ownLedger && keyLedger {
		return ProjectJoinRefusal{Reason: "both keep a ledger, and two ledgers cannot be merged"}
	}
	identity, err := hostIdentityTx(tx)
	if err != nil {
		return err
	}
	move := ProjectMove{From: id, To: key, ChangedAt: r.now().Truncate(time.Millisecond), OriginHost: identity}
	held, err := projectHoldings(tx, id)
	if err != nil {
		return err
	}
	target, err := projectHoldings(tx, key)
	if err != nil {
		return err
	}
	if reason := held.clash(target, move); reason != "" {
		return ProjectJoinRefusal{Reason: reason}
	}
	if _, err := tx.Exec(`UPDATE projects SET project_key=?, repository='', joined=1 WHERE id=?`, key, id); err != nil {
		return err
	}
	if err := moveLedgerTx(tx, own, key, ownLedger); err != nil {
		return err
	}
	if err := moveProjectRowsTx(tx, move, held); err != nil {
		return err
	}
	if err := moveProjectLinksTx(tx, move); err != nil {
		return err
	}
	if err := moveProjectSettingsTx(tx, move); err != nil {
		return err
	}
	if err := moveProjectTilesTx(tx, move); err != nil {
		return err
	}
	if held.lead {
		if _, err := publishCoordinatorTx(tx, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// UnjoinProject makes a joined checkout a project of its own again, under
// its own id; the project it left keeps everything held under its key.
func (r *Registry) UnjoinProject(id string) error {
	result, err := r.db.Exec(`UPDATE projects SET project_key='', repository='', joined=0 WHERE id=? AND added=1 AND joined=1`, id)
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

package ledger

import (
	"database/sql"
	"errors"
)

func replayable(q querier, projectKey string) error {
	var seq int64
	var typ string
	var size int
	err := q.QueryRow(`SELECT seq, type, length(payload) FROM ledger_events WHERE project_key=? AND length(payload)>? ORDER BY seq LIMIT 1`, projectKey, MaxEventBytes).Scan(&seq, &typ, &size)
	switch {
	case err == nil:
		return refuse("event %d (%s) in %s holds %d bytes, over the %d-byte limit one replay carries; it cannot sync, so %s stays local", seq, typ, projectKey, size, MaxEventBytes, projectKey)
	case errors.Is(err, sql.ErrNoRows):
		return nil
	}
	return err
}

func stampOrigins(tx *sql.Tx, projectKey, self string) error {
	_, err := tx.Exec(`UPDATE ledger_events SET actor_host_id=?, origin_seq=seq WHERE project_key=? AND origin_seq=0`, self, projectKey)
	return err
}

func currentOwner(q querier, projectKey string) (string, error) {
	var current string
	err := q.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, projectKey).Scan(&current)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return current, err
}

func assignable(q querier, projectKey, owner string) (already bool, err error) {
	self, err := hostID(q)
	if err != nil {
		return false, err
	}
	if owner == self {
		return false, refuse("this host cannot be its own remote owner; run ledger owner on the other host")
	}
	current, err := currentOwner(q, projectKey)
	if err != nil {
		return false, err
	}
	switch {
	case current == owner:
		return true, nil
	case current != "":
		return false, refuse("%s already syncs with %s; moving a ledger to another owner is not supported", projectKey, current)
	}
	return false, replayable(q, projectKey)
}

func claimable(q querier, projectKey, forHost string, adopting bool) (owned bool, err error) {
	self, err := hostID(q)
	if err != nil {
		return false, err
	}
	current, err := currentOwner(q, projectKey)
	if err != nil {
		return false, err
	}
	switch current {
	case self:
		return true, nil
	case "":
	default:
		return false, refuse("%s is owned by %s, not by this host", projectKey, current)
	}
	if adopting {
		var others int
		if err := q.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND actor_host_id!=?`, projectKey, forHost).Scan(&others); err != nil {
			return false, err
		}
		if others > 0 {
			return false, refuse("both hosts already hold ledger history for %s; the owner cannot adopt another host's history on top of its own", projectKey)
		}
	}
	return false, replayable(q, projectKey)
}

func (s *Store) CanAssignOwner(projectKey, owner string) error {
	_, err := assignable(s.read, projectKey, owner)
	return err
}

func (s *Store) AssignOwner(projectKey, owner string) error {
	return s.syncWrite(projectKey, true, func(tx *sql.Tx) error {
		already, err := assignable(tx, projectKey, owner)
		if err != nil || already {
			return err
		}
		self, err := hostID(tx)
		if err != nil {
			return err
		}
		if err := stampOrigins(tx, projectKey, self); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO ledger_state(project_key,owner_host_id,owner_cursor) VALUES(?,?,0)
			ON CONFLICT(project_key) DO UPDATE SET owner_host_id=excluded.owner_host_id, owner_cursor=0`, projectKey, owner)
		return err
	})
}

func (s *Store) ReleaseOwner(projectKey, owner string) error {
	return s.syncWrite(projectKey, true, func(tx *sql.Tx) error {
		current, err := currentOwner(tx, projectKey)
		if err != nil || current == "" {
			return err
		}
		if current != owner {
			return refuse("%s syncs with %s, not %s", projectKey, current, owner)
		}
		var shared int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND owner_seq>0`, projectKey).Scan(&shared); err != nil {
			return err
		}
		if shared > 0 {
			return refuse("%s already shares %d events with %s; it cannot go back to keeping its ledger alone", projectKey, shared, owner)
		}
		_, err = tx.Exec(`UPDATE ledger_state SET owner_host_id='', owner_cursor=0 WHERE project_key=?`, projectKey)
		return err
	})
}

func (s *Store) HoldsUnsharedIDs(projectKey string) (bool, error) {
	rows, err := s.read.Query(`SELECT DISTINCT type FROM ledger_events WHERE project_key=? AND owner_seq=0`, projectKey)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var typ string
		if err := rows.Scan(&typ); err != nil {
			return false, err
		}
		if IssuesID(EventType(typ)) {
			return true, nil
		}
	}
	return false, rows.Err()
}

func (s *Store) CheckClaim(projectKey, forHost string, adopting bool) error {
	_, err := claimable(s.read, projectKey, forHost, adopting)
	return err
}

func (s *Store) ClaimOwnership(projectKey, forHost string, adopting bool) error {
	return s.syncWrite(projectKey, false, func(tx *sql.Tx) error {
		owned, err := claimable(tx, projectKey, forHost, adopting)
		if err != nil || owned {
			return err
		}
		self, err := hostID(tx)
		if err != nil {
			return err
		}
		if err := stampOrigins(tx, projectKey, self); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO ledger_state(project_key,owner_host_id) VALUES(?,?)
			ON CONFLICT(project_key) DO UPDATE SET owner_host_id=excluded.owner_host_id`, projectKey, self)
		return err
	})
}

package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

var issuesID = map[EventType]bool{
	EventGoal: true, EventAsk: true, EventClause: true, EventCut: true,
	EventDecision: true, EventWorkItem: true, EventQuestion: true,
}

func IssuesID(t EventType) bool { return issuesID[t] }

type SyncStatus struct {
	ProjectKey string `json:"projectKey"`
	Owner      string `json:"owner"`
	Cursor     int64  `json:"cursor"`
	Unreplayed int    `json:"unreplayed"`
}

func hostID(q querier) (string, error) {
	var id string
	err := q.QueryRow(`SELECT host_id FROM host_identity WHERE id=1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) || id == "" {
		return "", fmt.Errorf("this Agenthail has no host identity")
	}
	return id, err
}

func (s *Store) Owner(projectKey string) (string, int64, error) {
	var owner string
	var cursor int64
	err := s.read.QueryRow(`SELECT owner_host_id, owner_cursor FROM ledger_state WHERE project_key=?`, projectKey).Scan(&owner, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return "", 0, nil
	}
	return owner, cursor, err
}

func (s *Store) RemoteOwner(projectKey string) (string, error) {
	owner, _, err := s.Owner(projectKey)
	if err != nil || owner == "" {
		return "", err
	}
	self, err := hostID(s.read)
	if err != nil || owner == self {
		return "", err
	}
	return owner, nil
}

func (s *Store) ProjectsPlannedHere(typ EventType) ([]string, error) {
	keys, err := s.ProjectsWith(typ)
	if err != nil {
		return nil, err
	}
	here := keys[:0]
	for _, key := range keys {
		owner, err := s.RemoteOwner(key)
		if err != nil {
			return nil, err
		}
		if owner == "" {
			here = append(here, key)
		}
	}
	return here, nil
}

func (s *Store) RemotelyOwned() ([]string, error) {
	self, err := hostID(s.read)
	if err != nil {
		return nil, err
	}
	return s.projectKeys(`SELECT project_key FROM ledger_state WHERE owner_host_id NOT IN ('', ?) ORDER BY project_key`, self)
}

func (s *Store) SyncedProjects(owner string) ([]string, error) {
	return s.projectKeys(`SELECT project_key FROM ledger_state WHERE owner_host_id=? ORDER BY project_key`, owner)
}

func (s *Store) projectKeys(query string, args ...any) ([]string, error) {
	rows, err := s.read.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var keys []string
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, err
		}
		keys = append(keys, key)
	}
	return keys, rows.Err()
}

func (s *Store) Status(projectKey string) (SyncStatus, error) {
	owner, cursor, err := s.Owner(projectKey)
	if err != nil {
		return SyncStatus{}, err
	}
	status := SyncStatus{ProjectKey: projectKey, Owner: owner, Cursor: cursor}
	self, err := hostID(s.read)
	if err != nil || owner == "" || owner == self {
		return status, err
	}
	err = s.read.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND actor_host_id=? AND owner_seq=0`, projectKey, self).Scan(&status.Unreplayed)
	return status, err
}

func (s *Store) HoldsHistory(projectKey string) (bool, error) {
	var n int
	err := s.read.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=?`, projectKey).Scan(&n)
	return n > 0, err
}

// syncWrite commits fn under the project's board lock. A write that can put events before ones already applied drops the cached board, so the next read rebuilds it in owner order.
func (s *Store) syncWrite(projectKey string, reorders bool, fn func(tx *sql.Tx) error) error {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return err
	}
	defer release()
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.cache == nil {
		return nil
	}
	if reorders {
		delete(s.cache.boards, projectKey)
		return nil
	}
	return s.catchUp(board, projectKey)
}

func (s *Store) AssignOwner(projectKey, owner string) error {
	return s.syncWrite(projectKey, true, func(tx *sql.Tx) error {
		self, err := hostID(tx)
		if err != nil {
			return err
		}
		if owner == self {
			return refuse("this host cannot be its own remote owner; run ledger owner on the other host")
		}
		var current string
		if err := tx.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, projectKey).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		switch {
		case current == owner:
			return nil
		case current != "":
			return refuse("%s already syncs with %s; moving a ledger to another owner is not supported", projectKey, current)
		}
		if _, err := tx.Exec(`UPDATE ledger_events SET actor_host_id=?, origin_seq=seq WHERE project_key=? AND origin_seq=0`, self, projectKey); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO ledger_state(project_key,owner_host_id,owner_cursor) VALUES(?,?,0)
			ON CONFLICT(project_key) DO UPDATE SET owner_host_id=excluded.owner_host_id, owner_cursor=0`, projectKey, owner)
		return err
	})
}

func (s *Store) ClaimOwnership(projectKey, forHost string, adopting bool) error {
	return s.syncWrite(projectKey, false, func(tx *sql.Tx) error {
		self, err := hostID(tx)
		if err != nil {
			return err
		}
		var current string
		if err := tx.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, projectKey).Scan(&current); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if current != "" && current != self {
			return refuse("%s is owned by %s, not by this host", projectKey, current)
		}
		if adopting {
			var others int
			if err := tx.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND actor_host_id!=?`, projectKey, forHost).Scan(&others); err != nil {
				return err
			}
			if others > 0 {
				return refuse("both hosts already hold ledger history for %s; the owner cannot adopt another host's history on top of its own", projectKey)
			}
		}
		if _, err := tx.Exec(`UPDATE ledger_events SET actor_host_id=?, origin_seq=seq WHERE project_key=? AND origin_seq=0`, self, projectKey); err != nil {
			return err
		}
		_, err = tx.Exec(`INSERT INTO ledger_state(project_key,owner_host_id) VALUES(?,?)
			ON CONFLICT(project_key) DO UPDATE SET owner_host_id=excluded.owner_host_id`, projectKey, self)
		return err
	})
}

func plannedHere(q querier, projectKey string) error {
	var owner string
	if err := q.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, projectKey).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if owner == "" {
		return nil
	}
	self, err := hostID(q)
	if err != nil {
		return err
	}
	if owner != self {
		return refuse("the ledger for %s is kept on host %s; a write that issues an ID is planned there, not here", projectKey, owner)
	}
	return nil
}

func requireOwner(tx querier, projectKey string) error {
	self, err := hostID(tx)
	if err != nil {
		return err
	}
	var owner string
	if err := tx.QueryRow(`SELECT owner_host_id FROM ledger_state WHERE project_key=?`, projectKey).Scan(&owner); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if owner != self {
		return refuse("this host does not own the ledger for %s", projectKey)
	}
	return nil
}

func (s *Store) AcceptEvents(projectKey, fromHost string, events []Event) ([]int64, error) {
	seqs := make([]int64, len(events))
	err := s.syncWrite(projectKey, false, func(tx *sql.Tx) error {
		if err := requireOwner(tx, projectKey); err != nil {
			return err
		}
		var others int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM ledger_events WHERE project_key=? AND actor_host_id!=?`, projectKey, fromHost).Scan(&others); err != nil {
			return err
		}
		for i, e := range events {
			if e.OriginSeq <= 0 || e.Type == "" || !json.Valid(e.Payload) {
				return refuse("replayed event %d has no origin, no type or a payload that is not JSON", i)
			}
			seq, err := existingEvent(tx, projectKey, fromHost, e)
			if err != nil {
				return err
			}
			if seq == 0 {
				if IssuesID(e.Type) && others > 0 {
					return refuse("a %s event issues an ID, so it is written through the owner; only a host adopting its history into an empty ledger replays one", e.Type)
				}
				e.ProjectKey, e.ActorHostID, e.OwnerSeq = projectKey, fromHost, 0
				inserted, err := insertEvent(tx, `INSERT`, e)
				if err != nil {
					return err
				}
				seq = inserted.Seq
			}
			seqs[i] = seq
		}
		return nil
	})
	return seqs, err
}

func existingEvent(tx querier, projectKey, host string, e Event) (int64, error) {
	var seq int64
	err := tx.QueryRow(`SELECT seq FROM ledger_events WHERE project_key=? AND actor_host_id=? AND origin_seq=?`, projectKey, host, e.OriginSeq).Scan(&seq)
	if errors.Is(err, sql.ErrNoRows) && e.Ref != "" {
		err = tx.QueryRow(`SELECT seq FROM ledger_events WHERE project_key=? AND ref=?`, projectKey, e.Ref).Scan(&seq)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return seq, err
}

func (s *Store) ExportEvents(projectKey string, after int64, limit int) ([]Event, error) {
	if err := requireOwner(s.read, projectKey); err != nil {
		return nil, err
	}
	return s.Events(projectKey, after, limit)
}

func (s *Store) PendingReplay(projectKey string, limit int) ([]Event, error) {
	self, err := hostID(s.read)
	if err != nil {
		return nil, err
	}
	rows, err := s.read.Query(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND actor_host_id=? AND owner_seq=0 ORDER BY seq LIMIT ?`, projectKey, self, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *Store) MarkReplayed(projectKey string, events []Event, ownerSeqs []int64) error {
	if len(events) != len(ownerSeqs) {
		return fmt.Errorf("replay answered %d events for %d sent", len(ownerSeqs), len(events))
	}
	return s.syncWrite(projectKey, true, func(tx *sql.Tx) error {
		self, err := hostID(tx)
		if err != nil {
			return err
		}
		for i, e := range events {
			if _, err := tx.Exec(`UPDATE ledger_events SET owner_seq=? WHERE project_key=? AND actor_host_id=? AND origin_seq=?`, ownerSeqs[i], projectKey, self, e.OriginSeq); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *Store) ApplyPulled(projectKey, owner string, events []Event) error {
	if len(events) == 0 {
		return nil
	}
	return s.syncWrite(projectKey, true, func(tx *sql.Tx) error {
		var current string
		var cursor int64
		if err := tx.QueryRow(`SELECT owner_host_id, owner_cursor FROM ledger_state WHERE project_key=?`, projectKey).Scan(&current, &cursor); err != nil {
			return err
		}
		if current != owner {
			return fmt.Errorf("%s no longer syncs with %s", projectKey, owner)
		}
		for _, e := range events {
			if e.Seq <= cursor {
				continue
			}
			if e.OriginSeq <= 0 || e.ActorHostID == "" {
				return fmt.Errorf("owner event %d has no origin", e.Seq)
			}
			result, err := tx.Exec(`UPDATE ledger_events SET owner_seq=? WHERE project_key=? AND actor_host_id=? AND origin_seq=?`, e.Seq, projectKey, e.ActorHostID, e.OriginSeq)
			if err != nil {
				return err
			}
			if n, err := result.RowsAffected(); err != nil || n > 0 {
				cursor = e.Seq
				continue
			}
			pulled := Event{ProjectKey: projectKey, Type: e.Type, Payload: e.Payload, ActorSessionID: e.ActorSessionID, ActorHostID: e.ActorHostID,
				OwnerSeq: e.Seq, OriginSeq: e.OriginSeq, Ref: e.Ref, CreatedAt: e.CreatedAt}
			inserted, err := insertEvent(tx, `INSERT OR IGNORE`, pulled)
			if err != nil {
				return err
			}
			if inserted.Seq == 0 && e.Ref != "" {
				if _, err := tx.Exec(`UPDATE ledger_events SET owner_seq=? WHERE project_key=? AND ref=?`, e.Seq, projectKey, e.Ref); err != nil {
					return err
				}
			}
			cursor = e.Seq
		}
		_, err := tx.Exec(`UPDATE ledger_state SET owner_cursor=? WHERE project_key=?`, cursor, projectKey)
		return err
	})
}

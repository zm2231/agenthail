package ledger

import (
	"database/sql"
	"errors"
)

type changeTx struct {
	*sql.Tx
	store      *Store
	projectKey string
	synced     bool
}

// OnChange runs fn with the project key after every write this store commits for that project.
func (s *Store) OnChange(fn func(projectKey string)) {
	s.changed.Store(&fn)
}

// OnRecord runs fn with the project key after every write this host makes to
// that project itself, leaving out what syncing with the project's owner
// writes.
func (s *Store) OnRecord(fn func(projectKey string)) {
	s.recorded.Store(&fn)
}

func (s *Store) notifyRecorded(projectKey string) {
	if fn := s.recorded.Load(); fn != nil {
		(*fn)(projectKey)
	}
}

func (s *Store) notify(projectKey string) {
	if fn := s.changed.Load(); fn != nil {
		(*fn)(projectKey)
	}
}

func (s *Store) begin(projectKey string) (*changeTx, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	if err := notMoved(tx, projectKey); err != nil {
		tx.Rollback()
		return nil, err
	}
	return &changeTx{Tx: tx, store: s, projectKey: projectKey}, nil
}

func notMoved(q querier, projectKey string) error {
	var to string
	err := q.QueryRow(`SELECT to_key FROM project_moves WHERE from_key=? AND to_key!=''`, projectKey).Scan(&to)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return err
	}
	return &Refusal{Reason: "the project " + projectKey + " is now " + to + " because its remote changed; nothing was recorded under the old key", MovedTo: to}
}

func (tx *changeTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.store.notify(tx.projectKey)
	if !tx.synced {
		tx.store.notifyRecorded(tx.projectKey)
	}
	return nil
}

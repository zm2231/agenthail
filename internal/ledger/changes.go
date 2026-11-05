package ledger

import "database/sql"

type changeTx struct {
	*sql.Tx
	store      *Store
	projectKey string
}

// OnChange runs fn with the project key after every write this store commits for that project.
func (s *Store) OnChange(fn func(projectKey string)) {
	s.changed.Store(&fn)
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
	return &changeTx{Tx: tx, store: s, projectKey: projectKey}, nil
}

func (tx *changeTx) Commit() error {
	if err := tx.Tx.Commit(); err != nil {
		return err
	}
	tx.store.notify(tx.projectKey)
	return nil
}

package ledger

import (
	"database/sql"
	"encoding/json"
	"sync"
	"time"
)

type boardCache struct {
	mu     sync.Mutex
	boards map[string]*Board
}

func NewCachedStore(db, read *sql.DB) *Store {
	return &Store{db: db, read: read, cache: &boardCache{boards: map[string]*Board{}}}
}

// lockBoard returns the project's board caught up to every committed event; the board advances only through catchUp.
func (s *Store) lockBoard(projectKey string) (*Board, func(), error) {
	if s.cache == nil {
		board, err := loadBoard(s.read, projectKey)
		return board, func() {}, err
	}
	s.cache.mu.Lock()
	board := s.cache.boards[projectKey]
	if board == nil {
		board = NewBoard()
		s.cache.boards[projectKey] = board
	}
	if err := s.catchUp(board, projectKey); err != nil {
		s.cache.mu.Unlock()
		return nil, nil, err
	}
	return board, s.cache.mu.Unlock, nil
}

func (s *Store) catchUp(board *Board, projectKey string) error {
	rows, err := s.read.Query(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND seq>? ORDER BY seq`, projectKey, board.AppliedSeq())
	if err != nil {
		return err
	}
	var events []Event
	for rows.Next() {
		e, err := scanEvent(rows)
		if err != nil {
			rows.Close()
			return err
		}
		events = append(events, e)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, e := range events {
		if err := board.Apply(e); err != nil {
			if s.cache != nil {
				delete(s.cache.boards, projectKey)
			}
			return err
		}
	}
	return nil
}

type plannedEvent func(board *Board, ids *idIssuer) (EventType, any, error)

func (s *Store) writePlanned(projectKey, actor string, now time.Time, plan plannedEvent) (*Board, func(), error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return nil, nil, err
	}
	fail := func(err error) (*Board, func(), error) {
		release()
		return nil, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fail(err)
	}
	defer tx.Rollback()
	typ, payload, err := plan(board, &idIssuer{q: tx, projectKey: projectKey})
	if err != nil {
		return fail(err)
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fail(err)
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: typ, Payload: data, ActorSessionID: actor, CreatedAt: now.UTC()}); err != nil {
		return fail(err)
	}
	if err := tx.Commit(); err != nil {
		return fail(err)
	}
	if err := s.catchUp(board, projectKey); err != nil {
		return fail(err)
	}
	return board, release, nil
}

func (s *Store) Board(projectKey string) (*Board, error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return nil, err
	}
	release()
	return board, nil
}

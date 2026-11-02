package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
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
		loaded, err := loadBoard(s.read, projectKey)
		if err != nil {
			s.cache.mu.Unlock()
			return nil, nil, err
		}
		board = loaded
		s.cache.boards[projectKey] = board
	}
	if err := s.catchUp(board, projectKey); err != nil {
		s.cache.mu.Unlock()
		return nil, nil, err
	}
	return board, s.cache.mu.Unlock, nil
}

func (s *Store) catchUp(board *Board, projectKey string) error {
	err := applyCommitted(s.read, board, projectKey)
	if err != nil && s.cache != nil {
		delete(s.cache.boards, projectKey)
	}
	return err
}

func applyCommitted(read querier, board *Board, projectKey string) error {
	rows, err := read.Query(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND seq>? ORDER BY seq`, projectKey, board.AppliedSeq())
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
	replay := slices.ContainsFunc(events, func(e Event) bool { return e.Type == EventReclassified })
	for _, e := range events {
		if replay {
			break
		}
		err := board.Apply(e)
		replay = errors.Is(err, errNeedsReplay)
		if err != nil && !replay {
			return err
		}
	}
	if !replay {
		return nil
	}
	all, err := loadEvents(read, projectKey)
	if err != nil {
		return err
	}
	return board.Rebuild(all)
}

type plannedEvent func(board *Board, ids *idIssuer) (EventType, any, error)

func (s *Store) writePlanned(projectKey, actor string, now time.Time, plan plannedEvent) (*Board, func(), error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return nil, nil, err
	}
	if err := s.commitPlanned(board, projectKey, actor, now, plan); err != nil {
		release()
		return nil, nil, err
	}
	if err := s.catchUp(board, projectKey); err != nil {
		release()
		return nil, nil, err
	}
	return board, release, nil
}

func (s *Store) commitPlanned(board *Board, projectKey, actor string, now time.Time, plan plannedEvent) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	typ, payload, err := plan(board, &idIssuer{q: tx, projectKey: projectKey})
	if err != nil {
		return err
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: typ, Payload: data, ActorSessionID: actor, CreatedAt: now.UTC()}); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Board(projectKey string) (*Board, error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return nil, err
	}
	release()
	return board, nil
}

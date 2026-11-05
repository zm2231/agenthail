package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

const (
	LedgerUnset    = 0
	LedgerEnabled  = 1
	LedgerDisabled = 2
)

type CaptureState string

const (
	CaptureEnabled  CaptureState = "enabled"
	CaptureDisabled CaptureState = "disabled"
)

// At keeps millisecond precision; an event's own stamp resolves to the second.
type CapturePayload struct {
	State CaptureState `json:"state"`
	At    time.Time    `json:"at"`
}

// Scope is the project's capture state. Unset captures only the project's lead and asks nothing of it; Since is when the current enabled run began.
type Scope struct {
	State int
	Since time.Time
}

func (b *Board) applyCapture(event Event) error {
	var p CapturePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	switch p.State {
	case CaptureEnabled:
		if b.capture.State != LedgerEnabled {
			b.capture = Scope{State: LedgerEnabled, Since: p.At.UTC()}
		}
	case CaptureDisabled:
		b.capture = Scope{State: LedgerDisabled}
	}
	return nil
}

func (b *Board) Capture() Scope {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.capture
}

func (s *Store) Scope(projectKey string) (Scope, error) {
	if projectKey == "" {
		return Scope{}, nil
	}
	board, err := s.Board(projectKey)
	if err != nil {
		return Scope{}, err
	}
	return board.Capture(), nil
}

func (s *Store) Enabled(projectKey string) (bool, error) {
	scope, err := s.Scope(projectKey)
	return scope.State == LedgerEnabled, err
}

func (s *Store) EnabledProjects() (map[string]bool, error) {
	keys, err := s.ProjectsWith(EventCapture)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, key := range keys {
		on, err := s.Enabled(key)
		if err != nil {
			return nil, err
		}
		if on {
			enabled[key] = true
		}
	}
	return enabled, nil
}

func (s *Store) Enable(projectKey string) error {
	return s.setCapture(projectKey, CaptureEnabled)
}

func (s *Store) Disable(projectKey string) error {
	return s.setCapture(projectKey, CaptureDisabled)
}

// setCapture appends a capture event only when it changes the state. It issues no ID, so a replica writes it locally and replays it to the owner like the person's words.
func (s *Store) setCapture(projectKey string, state CaptureState) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return err
	}
	defer release()
	now := time.Now().UTC()
	data, err := json.Marshal(CapturePayload{State: state, At: now})
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if current, err := latestCapture(tx, projectKey); err != nil || current == state {
		return err
	}
	if _, err := insertEvent(tx, `INSERT`, Event{ProjectKey: projectKey, Type: EventCapture, Payload: data, CreatedAt: now}); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	return s.catchUp(board, projectKey)
}

// latestCapture reads the last capture event in board order under the write transaction, so another process's write since the board was read is seen.
func latestCapture(q querier, projectKey string) (CaptureState, error) {
	var payload []byte
	err := q.QueryRow(`SELECT payload FROM ledger_events WHERE project_key=? AND type=? ORDER BY owner_seq=0 DESC, owner_seq DESC, seq DESC LIMIT 1`, projectKey, string(EventCapture)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	var p CapturePayload
	return p.State, json.Unmarshal(payload, &p)
}

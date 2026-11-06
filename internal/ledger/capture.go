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

// AttachMode is when an enabled project asks its sessions to attach the person's words: with each message, or at the checkpoints a session already reaches (adding, claiming or submitting work, starting or compacting, and reading the board).
type AttachMode string

const (
	AttachEachPrompt    AttachMode = "prompt"
	AttachAtCheckpoints AttachMode = "checkpoints"
)

func ParseAttachMode(s string) (AttachMode, error) {
	switch m := AttachMode(s); m {
	case "":
		return AttachEachPrompt, nil
	case AttachEachPrompt, AttachAtCheckpoints:
		return m, nil
	}
	return "", fmt.Errorf("unknown attach mode %q; use prompt or checkpoints", s)
}

// At keeps millisecond precision; an event's own stamp resolves to the second.
type CapturePayload struct {
	State  CaptureState `json:"state"`
	Attach AttachMode   `json:"attach,omitempty"`
	At     time.Time    `json:"at"`
}

// Scope is the project's capture state. Unset captures only the project's lead, which is asked to attach but not held; Since is when the current enabled run began.
type Scope struct {
	State  int
	Since  time.Time
	Attach AttachMode
}

func (b *Board) applyCapture(event Event) error {
	var p CapturePayload
	if err := json.Unmarshal(event.Payload, &p); err != nil {
		return err
	}
	switch p.State {
	case CaptureEnabled:
		mode, _ := ParseAttachMode(string(p.Attach))
		if b.capture.State != LedgerEnabled {
			b.capture = Scope{State: LedgerEnabled, Since: p.At.UTC()}
		}
		b.capture.Attach = mode
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

func (s *Store) Enable(projectKey string, mode AttachMode) error {
	if _, err := ParseAttachMode(string(mode)); err != nil {
		return err
	}
	return s.setCapture(projectKey, CapturePayload{State: CaptureEnabled, Attach: mode})
}

func (s *Store) Disable(projectKey string) error {
	return s.setCapture(projectKey, CapturePayload{State: CaptureDisabled})
}

// setCapture appends a capture event only when it changes the state. It issues no ID, so a replica writes it locally and replays it to the owner like the person's words.
func (s *Store) setCapture(projectKey string, want CapturePayload) error {
	if projectKey == "" {
		return fmt.Errorf("project key is required")
	}
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return err
	}
	defer release()
	now := time.Now().UTC()
	want.At = now
	if want.State == CaptureEnabled {
		want.Attach, _ = ParseAttachMode(string(want.Attach))
	}
	data, err := json.Marshal(want)
	if err != nil {
		return err
	}
	tx, err := s.begin(projectKey)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if current, err := latestCapture(tx, projectKey); err != nil || (current.State == want.State && current.Attach == want.Attach) {
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
func latestCapture(q querier, projectKey string) (CapturePayload, error) {
	var payload []byte
	err := q.QueryRow(`SELECT payload FROM ledger_events WHERE project_key=? AND type=? ORDER BY owner_seq=0 DESC, owner_seq DESC, seq DESC LIMIT 1`, projectKey, string(EventCapture)).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return CapturePayload{}, nil
	}
	if err != nil {
		return CapturePayload{}, err
	}
	var p CapturePayload
	if err := json.Unmarshal(payload, &p); err != nil {
		return CapturePayload{}, err
	}
	if p.State == CaptureEnabled {
		p.Attach, _ = ParseAttachMode(string(p.Attach))
	}
	return p, nil
}

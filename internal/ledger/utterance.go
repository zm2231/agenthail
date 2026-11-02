package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	CapturedByHook     = "hook"
	CapturedByDelivery = "delivery"
	CapturedByJournal  = "journal"
)

type Utterance struct {
	ProjectKey string
	Ref        string
	SpeakerID  string
	TargetID   string
	Surface    string
	Text       string
	At         time.Time
	CapturedBy string
}

const EventCaptureMissed = "ledger.capture.missed"

const promptRefPrefix = "claude-prompt:"

func PromptRef(promptID string) string { return promptRefPrefix + promptID }

func DeliveryRef(intentUUID string) string { return "delivery:" + intentUUID }

func IsPromptRef(ref string) bool { return strings.HasPrefix(ref, promptRefPrefix) }

func (s *Store) HasRef(projectKey, ref string) (bool, error) {
	var one int
	err := s.read.QueryRow(`SELECT 1 FROM ledger_events WHERE project_key=? AND ref=?`, projectKey, ref).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (u Utterance) HookMissed(inserted bool) bool {
	return inserted && u.CapturedBy == CapturedByJournal && strings.HasPrefix(u.Ref, promptRefPrefix)
}

func (s *Store) RecordUtterance(u Utterance) (Event, bool, error) {
	if u.ProjectKey == "" || u.Text == "" || u.Ref == "" || u.CapturedBy == "" {
		return Event{}, false, fmt.Errorf("project key, ref, text and capturing producer are required")
	}
	if u.At.IsZero() {
		u.At = time.Now()
	}
	data, err := json.Marshal(UtterancePayload{
		Text:        u.Text,
		UtteranceID: u.Ref,
		SessionID:   u.SpeakerID,
		TargetID:    u.TargetID,
		Surface:     u.Surface,
		CapturedBy:  u.CapturedBy,
		TS:          u.At.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return Event{}, false, err
	}
	board, release, err := s.lockBoard(u.ProjectKey)
	if err != nil {
		return Event{}, false, err
	}
	defer release()
	e, inserted, err := s.insertUtterance(u, data)
	if err != nil {
		return Event{}, false, err
	}
	return e, inserted, s.catchUp(board, u.ProjectKey)
}

func (s *Store) insertUtterance(u Utterance, data []byte) (Event, bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, false, err
	}
	defer tx.Rollback()
	e, err := insertEvent(tx, `INSERT OR IGNORE`, Event{
		ProjectKey:     u.ProjectKey,
		Type:           EventUtterance,
		Payload:        data,
		ActorSessionID: u.SpeakerID,
		Ref:            u.Ref,
		CreatedAt:      time.Now().UTC(),
	})
	if err != nil {
		return Event{}, false, err
	}
	inserted := e.Seq != 0
	if !inserted {
		if e, err = scanEvent(tx.QueryRow(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND ref=?`, u.ProjectKey, u.Ref)); err != nil {
			return Event{}, false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Event{}, false, err
	}
	return e, inserted, nil
}

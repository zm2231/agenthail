package ledger

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type Utterance struct {
	ProjectKey string
	Ref        string
	SpeakerID  string
	TargetID   string
	Surface    string
	Text       string
	At         time.Time
	Backfilled bool
}

const EventCaptureMissed = "ledger.capture.missed"

const promptRefPrefix = "claude-prompt:"

func PromptRef(promptID string) string { return promptRefPrefix + promptID }

func DeliveryRef(intentUUID string) string { return "delivery:" + intentUUID }

func (s *Store) RecordUtterance(u Utterance) (Event, error) {
	if u.ProjectKey == "" || u.Text == "" || u.Ref == "" {
		return Event{}, fmt.Errorf("project key, ref and text are required")
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
		TS:          u.At.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return Event{}, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, err
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
		return Event{}, err
	}
	if e.Seq == 0 {
		if e, err = scanEvent(tx.QueryRow(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND ref=?`, u.ProjectKey, u.Ref)); err != nil {
			return Event{}, err
		}
	} else if u.Backfilled && strings.HasPrefix(u.Ref, promptRefPrefix) {
		if err := recordCaptureMissed(tx, u); err != nil {
			return Event{}, err
		}
	}
	return e, tx.Commit()
}

// recordCaptureMissed notes a typed prompt the hook never recorded; why it missed is not known when the journal finds it.
func recordCaptureMissed(tx *sql.Tx, u Utterance) error {
	payload, err := json.Marshal(map[string]string{"projectKey": u.ProjectKey, "ref": u.Ref, "sessionId": u.TargetID, "reason": "unknown"})
	if err != nil {
		return err
	}
	_, err = tx.Exec(`INSERT INTO daemon_events (event_type,entity_id,payload,created_at) VALUES (?,?,?,?)`, EventCaptureMissed, u.TargetID, payload, time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

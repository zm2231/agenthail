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

type MissedCapture struct {
	ID        int64
	SessionID string
	Payload   []byte
	At        time.Time
}

func (s *Store) RecordUtterance(u Utterance) (Event, *MissedCapture, error) {
	if u.ProjectKey == "" || u.Text == "" || u.Ref == "" {
		return Event{}, nil, fmt.Errorf("project key, ref and text are required")
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
		return Event{}, nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, nil, err
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
		return Event{}, nil, err
	}
	var missed *MissedCapture
	if e.Seq == 0 {
		if e, err = scanEvent(tx.QueryRow(`SELECT `+eventColumns+` FROM ledger_events WHERE project_key=? AND ref=?`, u.ProjectKey, u.Ref)); err != nil {
			return Event{}, nil, err
		}
	} else if u.Backfilled && strings.HasPrefix(u.Ref, promptRefPrefix) {
		if missed, err = recordCaptureMissed(tx, u); err != nil {
			return Event{}, nil, err
		}
	}
	if err := tx.Commit(); err != nil {
		return Event{}, nil, err
	}
	return e, missed, nil
}

func recordCaptureMissed(tx *sql.Tx, u Utterance) (*MissedCapture, error) {
	payload, err := json.Marshal(map[string]string{"projectKey": u.ProjectKey, "ref": u.Ref, "sessionId": u.TargetID, "reason": "unknown"})
	if err != nil {
		return nil, err
	}
	at := time.Now().UTC()
	result, err := tx.Exec(`INSERT INTO daemon_events (event_type,entity_id,payload,created_at) VALUES (?,?,?,?)`, EventCaptureMissed, u.TargetID, payload, at.Format(time.RFC3339Nano))
	if err != nil {
		return nil, err
	}
	id, err := result.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &MissedCapture{ID: id, SessionID: u.TargetID, Payload: payload, At: at}, nil
}

package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// CopyWindow absorbs the prompt hook running once per config root for one submission.
const (
	CopyWindow    = 2 * time.Second
	JournalWindow = 10 * time.Minute
)

type Utterance struct {
	ProjectKey string
	CaptureRef string
	JournalRef string
	SpeakerID  string
	TargetID   string
	Surface    string
	Text       string
	At         time.Time
}

func UtteranceMatchKey(targetID, text string) string {
	sum := sha256.Sum256([]byte(targetID + "\x00" + text))
	return hex.EncodeToString(sum[:])
}

func (s *Store) RecordUtterance(u Utterance) (Event, error) {
	if u.ProjectKey == "" || u.Text == "" {
		return Event{}, fmt.Errorf("project key and text are required")
	}
	if (u.CaptureRef == "") == (u.JournalRef == "") {
		return Event{}, fmt.Errorf("an utterance needs exactly one of a capture ref or a journal ref")
	}
	if u.At.IsZero() {
		u.At = time.Now()
	}
	own, other, ref := "capture_ref", "journal_ref", u.CaptureRef
	if u.JournalRef != "" {
		own, other, ref = "journal_ref", "capture_ref", u.JournalRef
	}
	key, at := UtteranceMatchKey(u.TargetID, u.Text), u.At.UnixMilli()

	tx, err := s.db.Begin()
	if err != nil {
		return Event{}, err
	}
	defer tx.Rollback()
	found := func(query string, args ...any) (Event, bool, error) {
		e, err := scanEvent(tx.QueryRow(`SELECT `+eventColumns+` FROM ledger_events WHERE `+query, args...))
		if errors.Is(err, sql.ErrNoRows) {
			return Event{}, false, nil
		}
		return e, err == nil, err
	}
	done := func(e Event) (Event, error) { return e, tx.Commit() }

	if e, ok, err := found(`project_key=? AND `+own+`=?`, u.ProjectKey, ref); err != nil || ok {
		if err != nil {
			return Event{}, err
		}
		return done(e)
	}
	if u.CaptureRef != "" {
		e, ok, err := found(`project_key=? AND match_key=? AND capture_ref!='' AND ABS(occurred_ms-?)<=? ORDER BY seq LIMIT 1`, u.ProjectKey, key, at, CopyWindow.Milliseconds())
		if err != nil {
			return Event{}, err
		}
		if ok {
			return done(e)
		}
	}
	e, ok, err := found(`project_key=? AND match_key=? AND `+own+`='' AND `+other+`!='' AND ABS(occurred_ms-?)<=? ORDER BY seq LIMIT 1`, u.ProjectKey, key, at, JournalWindow.Milliseconds())
	if err != nil {
		return Event{}, err
	}
	if ok {
		bind := `UPDATE ledger_events SET journal_ref=? WHERE seq=?`
		args := []any{ref, e.Seq}
		if u.CaptureRef != "" {
			bind = `UPDATE ledger_events SET capture_ref=?, occurred_ms=? WHERE seq=?`
			args = []any{ref, at, e.Seq}
		}
		if _, err := tx.Exec(bind, args...); err != nil {
			return Event{}, err
		}
		e, _, err = found(`seq=?`, e.Seq)
		if err != nil {
			return Event{}, err
		}
		return done(e)
	}

	data, err := json.Marshal(UtterancePayload{
		Text:        u.Text,
		UtteranceID: ref,
		SessionID:   u.SpeakerID,
		TargetID:    u.TargetID,
		Surface:     u.Surface,
		TS:          u.At.UTC().Format(time.RFC3339),
	})
	if err != nil {
		return Event{}, err
	}
	e, err = insertEvent(tx, Event{
		ProjectKey:     u.ProjectKey,
		Type:           EventUtterance,
		Payload:        data,
		ActorSessionID: u.SpeakerID,
		CaptureRef:     u.CaptureRef,
		JournalRef:     u.JournalRef,
		CreatedAt:      time.Now().UTC(),
	}, key, at)
	if err != nil {
		return Event{}, err
	}
	return done(e)
}

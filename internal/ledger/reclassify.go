package ledger

import (
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ReclassifiedPayload struct {
	UtteranceID string `json:"utteranceId"`
	Reason      string `json:"reason"`
}

func reclassifiedRef(ref string) string { return "reclassified:" + ref }

// The prompt hook cannot tell a scheduled or injected prompt from the person's; the transcript can.
func (s *Store) ReclassifyUtterance(projectKey, ref, reason string, now time.Time) (bool, error) {
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return false, err
	}
	defer release()
	if !board.hookCaptured(ref) {
		return false, nil
	}
	data, err := json.Marshal(ReclassifiedPayload{UtteranceID: ref, Reason: reason})
	if err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	e, err := insertEvent(tx, `INSERT OR IGNORE`, Event{ProjectKey: projectKey, Type: EventReclassified, Payload: data, Ref: reclassifiedRef(ref), CreatedAt: now.UTC()})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return e.Seq != 0, s.catchUp(board, projectKey)
}

func (s *Store) HoldsUncheckedHookCapture(sessionID string) (bool, error) {
	var one int
	err := s.read.QueryRow(`SELECT 1 FROM ledger_events u WHERE u.type=? AND u.ref LIKE ? AND json_extract(u.payload,'$.capturedBy')=? AND json_extract(u.payload,'$.targetId')=?
		AND NOT EXISTS (SELECT 1 FROM ledger_events r WHERE r.project_key=u.project_key AND r.ref=? || u.ref) LIMIT 1`,
		string(EventUtterance), promptRefPrefix+"%", CapturedByHook, sessionID, reclassifiedRef("")).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (b *Board) hookCaptured(ref string) bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	u := b.utterances[ref]
	return u != nil && u.CapturedBy == CapturedByHook
}

func (b *Board) applyReclassified(data []byte) error {
	var p ReclassifiedPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if b.utterances[p.UtteranceID] == nil {
		return nil
	}
	b.retract(p.UtteranceID, p.Reason)
	delete(b.utterances, p.UtteranceID)
	for i, id := range b.utteranceOrder {
		if id == p.UtteranceID {
			b.utteranceOrder = append(b.utteranceOrder[:i:i], b.utteranceOrder[i+1:]...)
			break
		}
	}
	return nil
}

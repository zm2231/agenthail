package ledger

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"time"
	"unicode/utf8"
)

const (
	ReplyFound = "found"
	ReplyNone  = "none"
	ReplyLate  = "late"

	JoinedByID   = "id"
	JoinedByTime = "time"

	ContextReplyRunes = 2000
)

// Found and none are read before the session's own entry for the message; late means that entry
// was not seen in time, and any reply is the last one before the capture time.
type ContextPayload struct {
	UtteranceID string  `json:"utteranceId"`
	Status      string  `json:"status"`
	JoinedBy    string  `json:"joinedBy,omitempty"`
	Reply       *Reply  `json:"reply,omitempty"`
	Quotes      []Quote `json:"quotes,omitempty"`
}

type Reply struct {
	ItemID string `json:"itemId"`
	TS     string `json:"ts"`
	Text   string `json:"text"`
	Runes  int    `json:"runes"`
	SHA256 string `json:"sha256"`
}

func NewReply(itemID, ts, full string) *Reply {
	sum := sha256.Sum256([]byte(full))
	runes := utf8.RuneCountInString(full)
	text := full
	if runes > ContextReplyRunes {
		text = string([]rune(full)[runes-ContextReplyRunes:])
	}
	return &Reply{ItemID: itemID, TS: ts, Text: text, Runes: runes, SHA256: hex.EncodeToString(sum[:])}
}

func contextRef(utteranceID string) string { return "context:" + utteranceID }

func (s *Store) RecordContext(projectKey string, p ContextPayload, now time.Time) (bool, error) {
	if p.Status != ReplyFound && p.Status != ReplyNone && p.Status != ReplyLate {
		return false, refuse("a message's context status is found, none or late, not %q", p.Status)
	}
	board, release, err := s.lockBoard(projectKey)
	if err != nil {
		return false, err
	}
	defer release()
	board.mu.RLock()
	known := board.utterances[p.UtteranceID] != nil
	board.mu.RUnlock()
	if !known {
		return false, refuse("%s has no message %s", projectKey, p.UtteranceID)
	}
	data, err := json.Marshal(p)
	if err != nil {
		return false, err
	}
	tx, err := s.begin(projectKey)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	e, err := insertEvent(tx, `INSERT OR IGNORE`, Event{ProjectKey: projectKey, Type: EventContext, Payload: data, Ref: contextRef(p.UtteranceID), CreatedAt: now.UTC()})
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return e.Seq != 0, s.catchUp(board, projectKey)
}

func (c *ContextPayload) clone() *ContextPayload {
	if c == nil {
		return nil
	}
	out := *c
	out.Quotes = slices.Clone(c.Quotes)
	if c.Reply != nil {
		reply := *c.Reply
		out.Reply = &reply
	}
	return &out
}

func clipReply(context *ContextPayload) *ContextPayload {
	out := context.clone()
	if out == nil || out.Reply == nil || len(out.Reply.Text) <= readReplyClip {
		return out
	}
	start := len(out.Reply.Text) - readReplyClip
	for start < len(out.Reply.Text) && !isRuneStart(out.Reply.Text[start]) {
		start++
	}
	out.Reply.Text = out.Reply.Text[start:]
	return out
}

func (b *Board) applyContext(data []byte) error {
	var p ContextPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return err
	}
	if u := b.utterances[p.UtteranceID]; u != nil && u.Context == nil {
		u.Context = &p
	}
	return nil
}

func (s *Store) UtteranceText(projectKey, utteranceID string) (string, bool, error) {
	var text sql.NullString
	err := s.read.QueryRow(`SELECT json_extract(payload,'$.text') FROM ledger_events WHERE project_key=? AND type=? AND ref=?`, projectKey, string(EventUtterance), utteranceID).Scan(&text)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return text.String, err == nil, err
}

func (s *Store) ContextFor(projectKey, utteranceID string) (ContextPayload, bool, error) {
	var data []byte
	err := s.read.QueryRow(`SELECT payload FROM ledger_events WHERE project_key=? AND ref=?`, projectKey, contextRef(utteranceID)).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextPayload{}, false, nil
	}
	if err != nil {
		return ContextPayload{}, false, err
	}
	var p ContextPayload
	if err := json.Unmarshal(data, &p); err != nil {
		return ContextPayload{}, false, err
	}
	return p, true, nil
}

func (s *Store) LocalWithoutContextSince(projectKey string, notBefore time.Time) ([]OwedSort, error) {
	if whole := notBefore.Truncate(time.Second); whole.Before(notBefore) {
		notBefore = whole.Add(time.Second)
	}
	rows, err := s.read.Query(`SELECT u.ref, u.seq, json_extract(u.payload,'$.ts'), json_extract(u.payload,'$.targetId') FROM ledger_events u WHERE u.project_key=? AND u.type=? AND u.actor_host_id=COALESCE((SELECT host_id FROM host_identity WHERE id=1),'')
		AND json_extract(u.payload,'$.ts')>=? AND NOT EXISTS (SELECT 1 FROM ledger_events c WHERE c.project_key=u.project_key AND c.ref=? || u.ref) ORDER BY u.seq`,
		projectKey, string(EventUtterance), notBefore.UTC().Format(time.RFC3339), contextRef(""))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var owed []OwedSort
	for rows.Next() {
		var o OwedSort
		var ts string
		if err := rows.Scan(&o.Ref, &o.Seq, &ts, &o.TargetID); err != nil {
			return nil, err
		}
		o.At, _ = time.Parse(time.RFC3339, ts)
		owed = append(owed, o)
	}
	return owed, rows.Err()
}

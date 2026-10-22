package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const journalPromptAt = `COALESCE(julianday(json_extract(j.payload,'$.ts')),julianday(j.observed_at),0)`

// journalPromptsSQL opens with the predicate of the session_journal_prompts
// indexes and orders by their key, so it walks one of them in order and stops
// at its limit; both must stay identical to the indexes. The indexes hold
// every journal column it reads, so it never visits the table. Its session
// conditions mirror surface.Session.Delegated.
func journalPromptsSQL(sessionScoped bool) string {
	session := ""
	if sessionScoped {
		session = "AND j.session_id=?1"
	}
	return `SELECT j.session_id,j.seq,COALESCE(json_extract(j.payload,'$.body'),''),COALESCE(json_extract(j.payload,'$.itemId'),''),COALESCE(json_extract(j.payload,'$.turnId'),''),COALESCE(json_extract(j.payload,'$.ts'),''),COALESCE(json_extract(j.payload,'$.truncated'),0),j.body_ref,` + journalPromptAt + `
		FROM session_journal j
		JOIN sessions s ON s.id=j.session_id
		LEFT JOIN session_origins o ON o.session_id=j.session_id
		WHERE j.kind IN ('message','text') AND json_extract(j.payload,'$.role')='user'
		AND COALESCE(json_extract(j.payload,'$.origin'),'') IN ('','human')
		AND s.host_id='' AND s.parent_session_id='' AND s.subagent_depth=0 AND s.source NOT IN ('exec','subAgent') AND COALESCE(o.via,'')!='agent'
		` + session + `
		AND (?2='' OR json_extract(j.payload,'$.body') LIKE ?2 ESCAPE '\')
		AND (?3='' OR ` + journalPromptAt + `<?4 OR (` + journalPromptAt + `=?4 AND (j.session_id<?3 OR (j.session_id=?3 AND j.seq<?5))))
		ORDER BY ` + journalPromptAt + ` DESC, j.session_id DESC, j.seq DESC
		LIMIT ?6`
}

type JournalPrompt struct {
	SessionID string
	Seq       uint64
	ItemID    string
	TurnID    string
	TS        string
	Body      string
	Truncated bool
	BodyRef   string
	Cursor    JournalPromptCursor
}

// JournalPromptCursor orders prompts newest first across sessions.
type JournalPromptCursor struct {
	At        float64
	SessionID string
	Seq       uint64
}

func (c JournalPromptCursor) IsZero() bool { return c.SessionID == "" }

func (c JournalPromptCursor) String() string {
	if c.IsZero() {
		return ""
	}
	return strconv.FormatFloat(c.At, 'g', -1, 64) + ":" + strconv.FormatUint(c.Seq, 10) + ":" + c.SessionID
}

func ParseJournalPromptCursor(raw string) (JournalPromptCursor, error) {
	if raw == "" {
		return JournalPromptCursor{}, nil
	}
	parts := strings.SplitN(raw, ":", 3)
	if len(parts) != 3 || parts[2] == "" {
		return JournalPromptCursor{}, fmt.Errorf("invalid prompt cursor")
	}
	at, err := strconv.ParseFloat(parts[0], 64)
	if err != nil {
		return JournalPromptCursor{}, fmt.Errorf("invalid prompt cursor")
	}
	seq, err := strconv.ParseUint(parts[1], 10, 64)
	if err != nil {
		return JournalPromptCursor{}, fmt.Errorf("invalid prompt cursor")
	}
	return JournalPromptCursor{At: at, SessionID: parts[2], Seq: seq}, nil
}

type JournalPromptQuery struct {
	SessionID string
	Text      string
	Before    JournalPromptCursor
	Limit     int
}

// JournalPrompts lists user messages, newest first, from this host's
// top-level sessions that no agent started; the caller tells typed prompts
// from injected text.
// Text matches the message's retained inline body, case-insensitively for
// ASCII.
func (r *Registry) JournalPrompts(query JournalPromptQuery) ([]JournalPrompt, error) {
	if query.Limit < 1 || query.Limit > 500 {
		return nil, fmt.Errorf("invalid prompt query limit")
	}
	pattern := ""
	if query.Text != "" {
		pattern = "%" + escapeLike(query.Text) + "%"
	}
	before := query.Before
	rows, err := r.read.Query(journalPromptsSQL(query.SessionID != ""), query.SessionID, pattern, before.SessionID, before.At, before.Seq, query.Limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var prompts []JournalPrompt
	for rows.Next() {
		var prompt JournalPrompt
		if err := rows.Scan(&prompt.SessionID, &prompt.Seq, &prompt.Body, &prompt.ItemID, &prompt.TurnID, &prompt.TS, &prompt.Truncated, &prompt.BodyRef, &prompt.Cursor.At); err != nil {
			return nil, err
		}
		prompt.Cursor.SessionID, prompt.Cursor.Seq = prompt.SessionID, prompt.Seq
		prompts = append(prompts, prompt)
	}
	return prompts, rows.Err()
}

// JournalTurnBefore names the turn the journal records just before turnID
// started. It reports found=false when the journal holds no row of turnID, and
// an empty previous turn when turnID is the first turn it holds.
func (r *Registry) JournalTurnBefore(sessionID, turnID string) (previous string, found bool, err error) {
	var first sql.NullInt64
	if err := r.read.QueryRow(`SELECT MIN(seq) FROM session_journal WHERE session_id=? AND json_extract(payload,'$.turnId')=?`, sessionID, turnID).Scan(&first); err != nil || !first.Valid {
		return "", false, err
	}
	err = r.read.QueryRow(`SELECT json_extract(payload,'$.turnId') FROM session_journal WHERE session_id=? AND seq<? AND COALESCE(json_extract(payload,'$.turnId'),'') NOT IN ('',?) ORDER BY seq DESC LIMIT 1`, sessionID, first.Int64, turnID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", true, nil
	}
	return previous, err == nil, err
}

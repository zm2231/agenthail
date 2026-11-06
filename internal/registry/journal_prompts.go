package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/tidwall/gjson"
)

// payload_role is written with each row; the other prompt columns are
// virtual, so they cost no table storage, and the two partial indexes store
// them for user messages only. SQLite computes a virtual column for every row
// it writes, so they parse only payloads whose stored role is user. A query
// over them reads plain index values instead of parsing each payload, which
// the pure-Go SQLite driver makes several times slower than the scan itself.
const journalPromptPayload = `CASE WHEN payload_role='user' AND json_valid(payload) THEN payload END`

var journalPromptColumns = []struct{ name, decl string }{
	{"payload_role", `TEXT NOT NULL DEFAULT ''`},
	{"payload_origin", `TEXT GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.origin'),'')) VIRTUAL`},
	{"payload_body", `TEXT GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.body'),'')) VIRTUAL`},
	{"payload_item_id", `TEXT GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.itemId'),'')) VIRTUAL`},
	{"payload_turn_id", `TEXT GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.turnId'),'')) VIRTUAL`},
	{"payload_ts", `TEXT GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.ts'),'')) VIRTUAL`},
	{"payload_truncated", `INTEGER GENERATED ALWAYS AS (COALESCE(json_extract(` + journalPromptPayload + `,'$.truncated'),0)) VIRTUAL`},
	{"payload_at", `REAL GENERATED ALWAYS AS (COALESCE(julianday(json_extract(` + journalPromptPayload + `,'$.ts')),julianday(observed_at),0)) VIRTUAL`},
}

const journalPromptIndexed = `kind, payload_role, payload_origin, payload_body, payload_item_id, payload_turn_id, payload_ts, payload_truncated, body_ref`

func journalRole(kind string, payload []byte) string {
	if kind != "message" && kind != "text" {
		return ""
	}
	role := gjson.GetBytes(payload, "role")
	if role.Type != gjson.String {
		return ""
	}
	return role.Str
}

func ensureJournalPromptIndexes(tx *sql.Tx, rebuild bool) error {
	if rebuild {
		if err := dropJournalPromptColumns(tx); err != nil {
			return err
		}
	}
	for _, column := range journalPromptColumns {
		if err := ensureColumn(tx, "session_journal", column.name, column.decl); err != nil {
			return err
		}
	}
	if rebuild {
		if _, err := tx.Exec(`UPDATE session_journal SET payload_role=json_extract(payload,'$.role') WHERE kind IN ('message','text') AND payload_codec=0 AND json_valid(payload) AND json_type(payload,'$.role')='text'`); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`
CREATE INDEX IF NOT EXISTS session_journal_prompts
	ON session_journal(session_id, payload_at DESC, seq DESC, ` + journalPromptIndexed + `)
	WHERE kind IN ('message','text') AND payload_role='user';
CREATE INDEX IF NOT EXISTS session_journal_prompts_at
	ON session_journal(payload_at DESC, session_id DESC, seq DESC, ` + journalPromptIndexed + `)
	WHERE kind IN ('message','text') AND payload_role='user';`)
	return err
}

func dropJournalPromptColumns(tx *sql.Tx) error {
	if _, err := tx.Exec(`DROP INDEX IF EXISTS session_journal_prompts; DROP INDEX IF EXISTS session_journal_prompts_at`); err != nil {
		return err
	}
	for _, column := range slices.Backward(journalPromptColumns) {
		found, err := columnExists(tx, "session_journal", column.name)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if _, err := tx.Exec(`ALTER TABLE session_journal DROP COLUMN ` + column.name); err != nil {
			return err
		}
	}
	return nil
}

// journalPromptsSQL opens with the predicate of the session_journal_prompts
// indexes and orders by their key, so it walks one of them in order and stops
// at its limit. The indexes hold every journal column it reads, so it never
// visits the table. Its session conditions mirror surface.Session.Delegated.
func journalPromptsSQL(sessionScoped bool) string {
	session := ""
	if sessionScoped {
		session = "AND j.session_id=?1"
	}
	return `SELECT j.session_id,j.seq,j.payload_body,j.payload_item_id,j.payload_turn_id,j.payload_ts,j.payload_truncated,j.body_ref,j.payload_at
		FROM session_journal j
		JOIN sessions s ON s.id=j.session_id
		LEFT JOIN session_origins o ON o.session_id=j.session_id
		WHERE j.kind IN ('message','text') AND j.payload_role='user'
		AND j.payload_origin IN ('','human')
		AND s.host_id='' AND s.parent_session_id='' AND s.subagent_depth=0 AND s.source NOT IN ('exec','subAgent') AND COALESCE(o.via,'')!='agent'
		` + session + `
		AND (?2='' OR j.payload_body LIKE ?2 ESCAPE '\')
		AND (?3='' OR j.payload_at<?4 OR (j.payload_at=?4 AND (j.session_id<?3 OR (j.session_id=?3 AND j.seq<?5))))
		ORDER BY j.payload_at DESC, j.session_id DESC, j.seq DESC
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
	return appendRows(nil, rows, func(row rowScanner) (JournalPrompt, error) {
		var prompt JournalPrompt
		err := row.Scan(&prompt.SessionID, &prompt.Seq, &prompt.Body, &prompt.ItemID, &prompt.TurnID, &prompt.TS, &prompt.Truncated, &prompt.BodyRef, &prompt.Cursor.At)
		prompt.Cursor.SessionID, prompt.Cursor.Seq = prompt.SessionID, prompt.Seq
		return prompt, err
	})
}

// JournalTurnBefore names the turn the journal records just before turnID
// started. It reports found=false when the journal holds no row of turnID, and
// an empty previous turn when turnID is the first turn it holds.
func (r *Registry) JournalTurnBefore(sessionID, turnID string) (previous string, found bool, err error) {
	var first sql.NullInt64
	if err := r.read.QueryRow(`SELECT MIN(seq) FROM session_journal WHERE session_id=? AND json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.turnId')=?`, sessionID, turnID).Scan(&first); err != nil || !first.Valid {
		return "", false, err
	}
	err = r.read.QueryRow(`SELECT json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.turnId') FROM session_journal WHERE session_id=? AND seq<? AND COALESCE(json_extract(CASE WHEN kind IN ('message','text') AND json_valid(payload) THEN payload END,'$.turnId'),'') NOT IN ('',?) ORDER BY seq DESC LIMIT 1`, sessionID, first.Int64, turnID).Scan(&previous)
	if errors.Is(err, sql.ErrNoRows) {
		return "", true, nil
	}
	return previous, err == nil, err
}

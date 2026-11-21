package registry

import (
	"database/sql"
	"errors"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) RecordCodexReadStates(states map[string]surface.ReadState, at time.Time) error {
	if len(states) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for sessionID, state := range states {
		if _, err := tx.Exec(`INSERT INTO codex_read_states (session_id,first_unread,revision,updated_at_ms)
 SELECT ?,?,?,? WHERE EXISTS (SELECT 1 FROM sessions WHERE id=?)
 ON CONFLICT(session_id) DO UPDATE SET first_unread=excluded.first_unread, revision=excluded.revision, updated_at_ms=excluded.updated_at_ms
 WHERE first_unread IS NOT excluded.first_unread OR revision IS NOT excluded.revision`,
			sessionID, state.FirstUnread, state.Revision, at.UnixMilli(), sessionID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Registry) CodexReadStates() (map[string]surface.ReadState, error) {
	rows, err := r.read.Query(`SELECT session_id,first_unread,revision FROM codex_read_states`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	states := map[string]surface.ReadState{}
	for rows.Next() {
		var id string
		var state surface.ReadState
		if err := rows.Scan(&id, &state.FirstUnread, &state.Revision); err != nil {
			return nil, err
		}
		states[id] = state
	}
	return states, rows.Err()
}

func (r *Registry) CodexReadState(sessionID string) (surface.ReadState, bool, error) {
	var state surface.ReadState
	err := r.read.QueryRow(`SELECT first_unread,revision FROM codex_read_states WHERE session_id=?`, sessionID).Scan(&state.FirstUnread, &state.Revision)
	if errors.Is(err, sql.ErrNoRows) {
		return surface.ReadState{}, false, nil
	}
	return state, err == nil, err
}

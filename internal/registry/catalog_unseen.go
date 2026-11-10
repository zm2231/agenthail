package registry

import (
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (r *Registry) LocalSessionIDs(kind string) (map[string]bool, error) {
	ids, err := queryRows(r.read, []string{}, func(row rowScanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	}, `SELECT id FROM sessions WHERE surface=? AND host_id=''`, kind)
	if err != nil {
		return nil, err
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	return known, nil
}

// An unseen conversation never overwrites a row, even one registered since it was found.
func (r *Registry) RecordUnseenSessions(sessions []surface.Session) (int, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	added := 0
	for _, s := range sessions {
		if s.RuntimeKind == "" || s.Transcript == "" {
			return 0, fmt.Errorf("record unseen session %s: needs a runtime kind and a transcript", s.ID)
		}
		lastActive := int64(0)
		if !s.LastActive.IsZero() {
			lastActive = s.LastActive.UnixMilli()
		}
		result, err := tx.Exec(`INSERT INTO sessions (id,surface,name,cwd,status,transcript,has_local,runtime_kind,last_active_ms,ended,entrypoint,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,1,?,datetime('now')) ON CONFLICT(id) DO NOTHING`,
			s.ID, string(s.Surface), s.Name, s.Cwd, string(surface.StatusOffline), s.Transcript, b2i(s.HasLocal), string(s.RuntimeKind), lastActive, s.Entrypoint)
		if err != nil {
			return 0, err
		}
		if inserted, _ := result.RowsAffected(); inserted > 0 {
			added++
		}
	}
	return added, tx.Commit()
}

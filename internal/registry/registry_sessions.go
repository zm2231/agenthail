package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func (r *Registry) RegisterSession(s surface.Session) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := registerSessionTx(tx, s); err != nil {
		return err
	}
	return tx.Commit()
}

func registerSessionTx(tx *sql.Tx, s surface.Session) error {
	if err := registerSessionRowTx(tx, s); err != nil {
		return err
	}
	return bindClaimedCredentialTx(tx, s.ID, time.Now())
}

func registerSessionRowTx(tx *sql.Tx, s surface.Session) error {
	if s.RuntimeKind == "" {
		return fmt.Errorf("register session %s: no runtime kind", s.ID)
	}
	var owner string
	if err := tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, s.ID).Scan(&owner); err != nil && err != sql.ErrNoRows {
		return err
	}
	if owner != "" {
		if err := releaseUnservedHostRowTx(tx, s.ID, owner); err != nil {
			return err
		}
	}
	if s.HolderPID > 0 {
		_, err := tx.Exec(`INSERT INTO sessions (id,surface,name,cwd,status,transcript,has_local,source,runtime_kind,updated_at)
			VALUES (?,?,?,?,?,?,?,?,?,datetime('now')) ON CONFLICT(id) DO NOTHING`,
			s.ID, string(s.Surface), s.Name, s.Cwd, string(s.Status), s.Transcript, b2i(s.HasLocal), s.Source, string(s.RuntimeKind))
		return err
	}
	if _, err := tx.Exec(sessionUpsertSQL, sessionWriteValues(s)...); err != nil {
		return err
	}
	if s.Runtime != nil {
		location, err := json.Marshal(s.Runtime.Location)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO session_runtime(session_id,runtime_launcher,runtime_location,runtime_focusable,updated_at) VALUES(?,?,?,?,datetime('now')) ON CONFLICT(session_id) DO UPDATE SET runtime_launcher=excluded.runtime_launcher,runtime_location=excluded.runtime_location,runtime_focusable=excluded.runtime_focusable,updated_at=datetime('now')`, s.ID, string(s.Runtime.Launcher), location, b2i(s.Runtime.Focusable)); err != nil {
			return err
		}
	}
	if s.Surface == surface.KindClaude {
		pids := make([]int, 0, 1+len(s.Holders))
		if s.PID > 0 {
			pids = append(pids, s.PID)
		}
		for _, holder := range s.Holders {
			pids = append(pids, holder.PID)
		}
		for _, pid := range pids {
			if _, err := tx.Exec(`UPDATE sessions SET pid=0,bridge='',updated_at=datetime('now') WHERE surface=? AND pid=? AND id<>?`, string(surface.KindClaude), pid, s.ID); err != nil {
				return err
			}
		}
	}
	return nil
}

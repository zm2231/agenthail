package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"

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
	lastActiveMS := int64(0)
	if !s.LastActive.IsZero() {
		lastActiveMS = s.LastActive.UnixMilli()
	}
	parentID, rootID, depth, nickname, role := subagentValues(s)
	_, err := tx.Exec(
		`INSERT INTO sessions (id,surface,name,cwd,pid,status,transcript,has_local,source,transport,runtime_kind,configured_model,bridge,forked_from,last_active_ms,parent_session_id,root_session_id,subagent_depth,agent_nickname,agent_role,updated_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,datetime('now'))
		 ON CONFLICT(id) DO UPDATE SET surface=excluded.surface,name=excluded.name,cwd=excluded.cwd,
		   pid=excluded.pid,status=excluded.status,transcript=excluded.transcript,
		   has_local=excluded.has_local,
		   source=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.source
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.source
		     WHEN excluded.source<>'' THEN excluded.source
		     ELSE sessions.source
		   END,
		   transport=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.transport
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.transport
		     WHEN excluded.transport<>'' THEN excluded.transport
		     ELSE sessions.transport
			   END,
			   runtime_kind=CASE
		     WHEN sessions.surface='codex' AND sessions.transport='managed' AND NOT (excluded.source='vscode' AND excluded.transport='desktop') THEN sessions.runtime_kind
		     WHEN sessions.surface='codex' AND sessions.transport='desktop' AND excluded.source='agenthail' AND excluded.transport='managed' THEN sessions.runtime_kind
		     ELSE excluded.runtime_kind
			   END,
			   configured_model=CASE WHEN excluded.configured_model<>'' THEN excluded.configured_model ELSE sessions.configured_model END,
			   bridge=excluded.bridge,
			   forked_from=CASE WHEN sessions.forked_from<>'' THEN sessions.forked_from ELSE excluded.forked_from END,
			   last_active_ms=excluded.last_active_ms,
		   parent_session_id=CASE WHEN excluded.parent_session_id<>'' THEN excluded.parent_session_id ELSE sessions.parent_session_id END,
		   root_session_id=CASE WHEN excluded.root_session_id<>'' THEN excluded.root_session_id ELSE sessions.root_session_id END,
		   subagent_depth=CASE WHEN excluded.parent_session_id<>'' THEN excluded.subagent_depth ELSE sessions.subagent_depth END,
		   agent_nickname=CASE WHEN excluded.agent_nickname<>'' THEN excluded.agent_nickname ELSE sessions.agent_nickname END,
		   agent_role=CASE WHEN excluded.agent_role<>'' THEN excluded.agent_role ELSE sessions.agent_role END,
		   updated_at=datetime('now')`,
		s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, string(s.Status), s.Transcript, b2i(s.HasLocal), s.Source, s.Transport, string(s.RuntimeKind), s.ConfiguredModel, s.Bridge, s.ForkedFrom, lastActiveMS, parentID, rootID, depth, nickname, role)
	if err != nil {
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

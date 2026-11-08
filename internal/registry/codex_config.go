package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"time"
)

func (r *Registry) CodexConfigOverrides(sessionID string) (map[string]any, error) {
	overrides := map[string]any{}
	var stored string
	err := r.read.QueryRow(`SELECT overrides FROM session_codex_config WHERE session_id=?`, sessionID).Scan(&stored)
	if errors.Is(err, sql.ErrNoRows) {
		return overrides, nil
	}
	if err != nil {
		return nil, err
	}
	return overrides, json.Unmarshal([]byte(stored), &overrides)
}

func (r *Registry) SetCodexConfigOverride(sessionID, key string, value any) (map[string]any, error) {
	previous, err := r.CodexConfigOverrides(sessionID)
	if err != nil {
		return nil, err
	}
	overrides := maps.Clone(previous)
	if value == nil {
		delete(overrides, key)
	} else {
		overrides[key] = value
	}
	return previous, r.RestoreCodexConfigOverrides(sessionID, overrides)
}

func (r *Registry) RestoreCodexConfigOverrides(sessionID string, overrides map[string]any) error {
	if overrides == nil {
		overrides = map[string]any{}
	}
	stored, err := json.Marshal(overrides)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`INSERT INTO session_codex_config(session_id,overrides,updated_at_ms) VALUES(?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET overrides=excluded.overrides,updated_at_ms=excluded.updated_at_ms`,
		sessionID, string(stored), time.Now().UnixMilli())
	return err
}

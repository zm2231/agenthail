package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// A scripted run Agenthail didn't start stays registered, so its id is known, but no list shows it.
func hiddenScripted(entrypoint, runtimeKind, sessionID string) string {
	return fmt.Sprintf(`(%s IN ('sdk-cli','sdk-py') AND %s<>'claude-host' AND NOT EXISTS (SELECT 1 FROM session_origins o WHERE o.session_id=%s))`, entrypoint, runtimeKind, sessionID)
}

func hiddenSessionRow(alias string) string {
	return hiddenScripted(alias+".entrypoint", alias+".runtime_kind", alias+".id")
}

func (r *Registry) HiddenScripted(sessionID, entrypoint, runtimeKind string) (bool, error) {
	var hidden bool
	err := r.read.QueryRow(`SELECT `+hiddenScripted("?1", "?2", "?3"), entrypoint, runtimeKind, sessionID).Scan(&hidden)
	return hidden, err
}

func (r *Registry) HiddenCatalogRows(kind string) ([]string, error) {
	return queryRows(r.read, []string{}, func(row rowScanner) (string, error) {
		var id string
		return id, row.Scan(&id)
	}, `SELECT cs.session_id FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id
		WHERE s.surface=? AND s.host_id='' AND `+hiddenSessionRow("s")+` ORDER BY cs.session_id`, kind)
}

func (r *Registry) RemoveHiddenCatalogRows(ids []string) ([]CatalogEvent, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	events := []CatalogEvent{}
	for _, id := range ids {
		var kind, observedAt string
		err := tx.QueryRow(`SELECT s.surface,cs.observed_at FROM catalog_sessions cs JOIN sessions s ON s.id=cs.session_id WHERE cs.session_id=? AND s.host_id='' AND `+hiddenSessionRow("s"), id).Scan(&kind, &observedAt)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		payload, err := json.Marshal(map[string]string{"sessionId": id, "surface": kind, "reason": "scripted_run"})
		if err != nil {
			return nil, err
		}
		event, created, err := r.AppendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + id + ":" + observedAt, Type: "session.removed", EntityID: id, Payload: payload})
		if err != nil {
			return nil, err
		}
		if _, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, id); err != nil {
			return nil, err
		}
		if created {
			events = append(events, event)
		}
	}
	return events, tx.Commit()
}

func (r *Registry) SessionsWithoutEntrypoint(kind string) ([]CatalogEndedRow, error) {
	return r.endedRows(`SELECT id,transcript FROM sessions WHERE surface=? AND host_id='' AND transcript<>'' AND entrypoint='' ORDER BY id`, kind)
}

func (r *Registry) RecordEntrypoints(entrypoints map[string]string) error {
	if len(entrypoints) == 0 {
		return nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, id := range slices.Sorted(maps.Keys(entrypoints)) {
		entrypoint := strings.TrimSpace(entrypoints[id])
		if entrypoint == "" {
			continue
		}
		if _, err := tx.Exec(`UPDATE sessions SET entrypoint=?1 WHERE id=?2 AND entrypoint<>?1`, entrypoint, id); err != nil {
			return err
		}
	}
	return tx.Commit()
}

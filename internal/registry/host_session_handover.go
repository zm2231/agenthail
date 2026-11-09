package registry

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type MovedSession struct {
	SessionID   string `json:"sessionId"`
	MovedToHost string `json:"movedToHost"`
}

func (r *Registry) MovedOutSessions() ([]MovedSession, error) {
	return queryRows(r.read, []MovedSession{}, func(row rowScanner) (MovedSession, error) {
		var moved MovedSession
		return moved, row.Scan(&moved.SessionID, &moved.MovedToHost)
	}, `SELECT m.conversation,m.peer_host FROM session_moves m JOIN sessions s ON s.id=m.conversation
		WHERE m.role='out' AND m.state=? AND s.host_id<>''
		AND m.rowid=(SELECT o.rowid FROM session_moves o WHERE o.conversation=m.conversation AND o.role='out' AND o.state=? ORDER BY o.updated_at DESC, o.rowid DESC LIMIT 1)
		ORDER BY m.conversation`, MoveDone, MoveDone)
}

func (r *Registry) HandOverHostSession(from, sessionID, entityID, to string) (bool, error) {
	if from == "" || to == "" || from == to {
		return false, nil
	}
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner string
	err = tx.QueryRow(`SELECT host_id FROM sessions WHERE id=?`, sessionID).Scan(&owner)
	if errors.Is(err, sql.ErrNoRows) || err == nil && owner != from {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var fromName, toName string
	if err := tx.QueryRow(`SELECT name FROM hosts WHERE host_id=?`, from).Scan(&fromName); err != nil {
		return false, ignoreNoRows(err)
	}
	if err := tx.QueryRow(`SELECT name FROM hosts WHERE host_id=?`, to).Scan(&toName); err != nil {
		return false, ignoreNoRows(err)
	}
	result, err := tx.Exec(`DELETE FROM catalog_sessions WHERE session_id=?`, sessionID)
	if err != nil {
		return false, err
	}
	if removed, _ := result.RowsAffected(); removed > 0 {
		payload, _ := json.Marshal(map[string]string{"sessionId": entityID, "reason": "moved_to_host"})
		if _, _, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "session.removed:" + entityID + ":handover:" + to, Type: "session.removed", EntityID: entityID, Payload: payload}); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`UPDATE sessions SET host_id=?,updated_at=datetime('now') WHERE id=? AND host_id=?`, to, sessionID, from); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	r.signalChange(ChangeHostDeliveries)
	_ = r.RecordHistory(HistoryEntry{Kind: "session-operation", SessionID: sessionID, Result: fmt.Sprintf("moved from host %s to host %s", fromName, toName)})
	return true, nil
}

func ignoreNoRows(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

type SilentHost struct {
	HostID      string    `json:"hostId"`
	Name        string    `json:"name"`
	LastContact time.Time `json:"lastContact"`
	Sessions    []string  `json:"sessions"`
}

func (r *Registry) SilentHostSessions(silentFor time.Duration, now time.Time) ([]SilentHost, error) {
	hosts, err := queryRows(r.read, []SilentHost{}, func(row rowScanner) (SilentHost, error) {
		var host SilentHost
		var contact string
		if err := row.Scan(&host.HostID, &host.Name, &contact); err != nil {
			return host, err
		}
		host.LastContact, _ = parseTimestamp(contact)
		return host, nil
	}, `SELECT host_id,name,last_contact_at FROM hosts WHERE state<>'connected' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	silent := []SilentHost{}
	for _, host := range hosts {
		if !host.LastContact.IsZero() && now.Sub(host.LastContact) < silentFor {
			continue
		}
		if host.Sessions, err = queryRows(r.read, []string{}, func(row rowScanner) (string, error) {
			var id string
			return id, row.Scan(&id)
		}, `SELECT id FROM sessions WHERE host_id=? ORDER BY id`, host.HostID); err != nil {
			return nil, err
		}
		if len(host.Sessions) > 0 {
			silent = append(silent, host)
		}
	}
	return silent, nil
}

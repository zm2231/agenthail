package registry

import "time"

// sessionStartWindow is how long one start of a session stays claimed.
// Claude runs every configured copy of a hook at once, so the copies of one
// start arrive within this window and a later start of the same kind does not.
const sessionStartWindow = 30 * time.Second

func (r *Registry) ClaimSessionStart(sessionID, key, fingerprint string, now time.Time) (bool, error) {
	at := now.UnixMilli()
	result, err := r.db.Exec(`INSERT INTO session_context(session_id,fingerprint,start_key,delivered_at_ms) VALUES(?,?,?,?)
		ON CONFLICT(session_id) DO UPDATE SET fingerprint=excluded.fingerprint,start_key=excluded.start_key,delivered_at_ms=excluded.delivered_at_ms
		WHERE session_context.start_key!=excluded.start_key OR session_context.delivered_at_ms<=?`,
		sessionID, fingerprint, key, at, at-sessionStartWindow.Milliseconds())
	if err != nil {
		return false, err
	}
	claimed, err := result.RowsAffected()
	return claimed == 1, err
}

type ContextChange int

const (
	ContextCurrent ContextChange = iota
	ContextNew
	ContextChanged
)

func (r *Registry) ClaimContextChange(sessionID, fingerprint string, now time.Time) (ContextChange, error) {
	result, err := r.db.Exec(`INSERT INTO session_context(session_id,fingerprint,delivered_at_ms) VALUES(?,?,?) ON CONFLICT(session_id) DO NOTHING`, sessionID, fingerprint, now.UnixMilli())
	if err != nil {
		return ContextCurrent, err
	}
	if inserted, err := result.RowsAffected(); err != nil || inserted == 1 {
		return ContextNew, err
	}
	result, err = r.db.Exec(`UPDATE session_context SET fingerprint=?,delivered_at_ms=? WHERE session_id=? AND fingerprint!=?`, fingerprint, now.UnixMilli(), sessionID, fingerprint)
	if err != nil {
		return ContextCurrent, err
	}
	if updated, err := result.RowsAffected(); err != nil || updated == 0 {
		return ContextCurrent, err
	}
	return ContextChanged, nil
}

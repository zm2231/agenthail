package registry

import (
	"database/sql"
	"time"
)

// PeerRelayMemory is how long a retired reply relay's socket still names its
// source: Claude reports a held message's expiry to the address it came from,
// which may be a relay retired since.
const PeerRelayMemory = time.Hour

func (r *Registry) RecordPeerRelay(socketPath, sessionID string, started time.Time) error {
	_, err := r.db.Exec(`INSERT INTO peer_relay_sockets(socket_path,session_id,started_ms) VALUES(?,?,?)`, socketPath, sessionID, started.UnixMilli())
	return err
}

func (r *Registry) RetirePeerRelay(socketPath string, retired time.Time) error {
	_, err := r.db.Exec(`UPDATE peer_relay_sockets SET retired_ms=? WHERE socket_path=? AND retired_ms=0`, retired.UnixMilli(), socketPath)
	return err
}

func (r *Registry) RetirePeerRelays(retired time.Time) error {
	if _, err := r.db.Exec(`UPDATE peer_relay_sockets SET retired_ms=? WHERE retired_ms=0`, retired.UnixMilli()); err != nil {
		return err
	}
	return r.ForgetPeerRelays(retired)
}

func (r *Registry) ForgetPeerRelays(now time.Time) error {
	_, err := r.db.Exec(`DELETE FROM peer_relay_sockets WHERE retired_ms>0 AND retired_ms<?`, now.Add(-PeerRelayMemory).UnixMilli())
	return err
}

// PeerRelaySession is the source session of the reply relay that listened on
// socketPath at, or retired no longer than PeerRelayMemory before it, or ""
// when none did.
func (r *Registry) PeerRelaySession(socketPath string, at time.Time) (string, error) {
	var session string
	err := r.db.QueryRow(`SELECT session_id FROM peer_relay_sockets WHERE socket_path=? AND started_ms<=? AND (retired_ms=0 OR retired_ms>=?) ORDER BY started_ms DESC, id DESC LIMIT 1`, socketPath, at.UnixMilli(), at.Add(-PeerRelayMemory).UnixMilli()).Scan(&session)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return session, err
}

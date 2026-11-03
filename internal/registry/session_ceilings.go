package registry

import (
	"bytes"
	"encoding/json"
)

const sessionCeilingOf = `COALESCE((SELECT c.ceiling FROM session_ceilings c WHERE c.session_id=?), '{}')`

func bounded(ceiling json.RawMessage) bool {
	trimmed := bytes.TrimSpace(ceiling)
	return len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("{}")) && !bytes.Equal(trimmed, []byte("null"))
}

// SessionCeiling is the session's recorded ceiling; {} is unbounded, as for every session a person starts.
func (r *Registry) SessionCeiling(sessionID string) (json.RawMessage, error) {
	var ceiling string
	err := r.read.QueryRow(`SELECT `+sessionCeilingOf, sessionID).Scan(&ceiling)
	return json.RawMessage(ceiling), err
}

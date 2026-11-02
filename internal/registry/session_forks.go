package registry

import (
	"database/sql"
	"errors"
	"time"
)

const maxForkDepth = 16

// Since is the latest registration on the path down to the fork.
type ForkAncestor struct {
	SessionID string
	Since     time.Time
}

func (r *Registry) ForkAncestors(sessionID string) ([]ForkAncestor, error) {
	var ancestors []ForkAncestor
	var since time.Time
	seen := map[string]bool{sessionID: true}
	current := sessionID
	for len(ancestors) < maxForkDepth {
		var parent, registered string
		err := r.read.QueryRow(`SELECT forked_from,registered_at FROM sessions WHERE id=?`, current).Scan(&parent, &registered)
		if errors.Is(err, sql.ErrNoRows) {
			return ancestors, nil
		}
		if err != nil {
			return nil, err
		}
		if parent == "" || seen[parent] {
			return ancestors, nil
		}
		at, err := time.Parse(time.DateTime, registered)
		if err != nil {
			return nil, err
		}
		if at.After(since) {
			since = at
		}
		ancestors = append(ancestors, ForkAncestor{SessionID: parent, Since: since.UTC()})
		seen[parent] = true
		current = parent
	}
	return ancestors, nil
}

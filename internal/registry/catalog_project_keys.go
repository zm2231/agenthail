package registry

import (
	"database/sql"
	"errors"
)

func (r *Registry) LocalCatalogProjectKey(key string) (bool, error) {
	var one int
	err := r.read.QueryRow(`SELECT 1 FROM catalog_sessions c JOIN sessions s ON s.id=c.session_id WHERE s.host_id='' AND json_valid(c.host_project) AND json_extract(c.host_project,'$.key')=? LIMIT 1`, key).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

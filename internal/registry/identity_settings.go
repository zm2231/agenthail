package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	SendModeQueue = "queue"
	SendModeNow   = "now"
)

func (r *Registry) SetIdentitySendMode(name, mode string) error {
	if mode != SendModeQueue && mode != SendModeNow {
		return fmt.Errorf("send mode is %s or %s, not %q", SendModeNow, SendModeQueue, mode)
	}
	id, err := agentIdentityIDTx(r.db, strings.TrimPrefix(name, "@"))
	if err != nil {
		return err
	}
	_, err = r.db.Exec(`INSERT INTO identity_settings(identity_id,send_mode,changed_at_ms) VALUES(?,?,?) ON CONFLICT(identity_id) DO UPDATE SET send_mode=excluded.send_mode,changed_at_ms=excluded.changed_at_ms`, id, mode, r.now().UnixMilli())
	return err
}

// IdentitySendMode is how a send from the identity with no mode goes; queue when the person set none.
func (r *Registry) IdentitySendMode(identityID string) (string, error) {
	var mode string
	err := r.read.QueryRow(`SELECT send_mode FROM identity_settings WHERE identity_id=?`, identityID).Scan(&mode)
	if errors.Is(err, sql.ErrNoRows) {
		return SendModeQueue, nil
	}
	return mode, err
}

package registry

import (
	"crypto/ed25519"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zm2231/agenthail/internal/hosts"
)

type HostKey = hosts.SigningKey

func ensureHostSigningKey(tx *sql.Tx, now time.Time) error {
	var current bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM host_signing_keys WHERE retired_at_ms=0)`).Scan(&current); err != nil || current {
		return err
	}
	return newHostSigningKeyTx(tx, now)
}

func newHostSigningKeyTx(tx *sql.Tx, now time.Time) error {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return fmt.Errorf("generate host signing key: %w", err)
	}
	_, err = tx.Exec(`INSERT INTO host_signing_keys(key_id,public_key,private_key,created_at_ms) VALUES(?,?,?,?)`, hosts.SigningKeyID(public), []byte(public), []byte(private), now.UnixMilli())
	return err
}

// Retired keys stay listed so what they signed still checks.
func (r *Registry) HostKeys() ([]HostKey, error) {
	rows, err := r.read.Query(`SELECT key_id,public_key,created_at_ms,retired_at_ms,revoked_at_ms FROM host_signing_keys ORDER BY created_at_ms,key_id`)
	if err != nil {
		return nil, err
	}
	return appendRows(nil, rows, scanHostKey)
}

func scanHostKey(row rowScanner) (HostKey, error) {
	var key HostKey
	var public []byte
	var created, retired, revoked int64
	if err := row.Scan(&key.KeyID, &public, &created, &retired, &revoked); err != nil {
		return HostKey{}, err
	}
	key.PublicKey, key.CreatedAt, key.RetiredAt, key.RevokedAt = ed25519.PublicKey(public), time.UnixMilli(created), fromUnixMilli(retired), fromUnixMilli(revoked)
	return key, nil
}

func hostSignerTx(tx *sql.Tx) (string, ed25519.PrivateKey, error) {
	var keyID string
	var private []byte
	if err := tx.QueryRow(`SELECT key_id,private_key FROM host_signing_keys WHERE retired_at_ms=0 AND private_key IS NOT NULL`).Scan(&keyID, &private); err != nil {
		return "", nil, fmt.Errorf("read host signing key: %w", err)
	}
	if len(private) != ed25519.PrivateKeySize {
		return "", nil, errors.New("host signing key is malformed")
	}
	return keyID, ed25519.PrivateKey(private), nil
}

func (r *Registry) RotateHostKey(revoke bool, now time.Time) (HostKey, []CatalogEvent, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return HostKey{}, nil, err
	}
	defer tx.Rollback()
	revokedAt := int64(0)
	if revoke {
		revokedAt = now.UnixMilli()
	}
	if _, err := tx.Exec(`UPDATE host_signing_keys SET retired_at_ms=?,revoked_at_ms=CASE WHEN ?>0 THEN ? ELSE revoked_at_ms END,private_key=NULL WHERE retired_at_ms=0`, now.UnixMilli(), revokedAt, revokedAt); err != nil {
		return HostKey{}, nil, err
	}
	if err := newHostSigningKeyTx(tx, now); err != nil {
		return HostKey{}, nil, err
	}
	rows, err := tx.Query(`SELECT key_id,public_key,created_at_ms,retired_at_ms,revoked_at_ms FROM host_signing_keys ORDER BY created_at_ms,key_id`)
	if err != nil {
		return HostKey{}, nil, err
	}
	keys, err := appendRows(nil, rows, scanHostKey)
	if err != nil {
		return HostKey{}, nil, err
	}
	current := keys[len(keys)-1]
	payload, _ := json.Marshal(map[string]any{"keys": keys})
	event, _, err := appendCatalogEventTx(tx, CatalogEvent{DedupeKey: "host.keys:" + current.KeyID, Type: "host.keys", EntityID: current.KeyID, Payload: payload})
	if err != nil {
		return HostKey{}, nil, err
	}
	return current, []CatalogEvent{event}, tx.Commit()
}

func (r *Registry) RecordPeerHostKeys(hostID string, keys []HostKey) error {
	tx, err := r.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var paired bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM hosts WHERE host_id=?)`, hostID).Scan(&paired); err != nil || !paired {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM peer_host_keys WHERE host_id=?`, hostID); err != nil {
		return err
	}
	for _, key := range keys {
		if !key.Valid() {
			continue
		}
		if _, err := tx.Exec(`INSERT INTO peer_host_keys(host_id,key_id,public_key,created_at_ms,retired_at_ms,revoked_at_ms) VALUES(?,?,?,?,?,?)`, hostID, key.KeyID, []byte(key.PublicKey), key.CreatedAt.UnixMilli(), unixMilli(key.RetiredAt), unixMilli(key.RevokedAt)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func peerHostKeyTx(q interface {
	QueryRow(string, ...any) *sql.Row
}, hostID, keyID string) (HostKey, error) {
	return scanHostKey(q.QueryRow(`SELECT key_id,public_key,created_at_ms,retired_at_ms,revoked_at_ms FROM peer_host_keys WHERE host_id=? AND key_id=?`, hostID, keyID))
}

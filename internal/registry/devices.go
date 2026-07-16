package registry

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
)

const deviceTokenPrefix = "ahd_"

var (
	ErrPairingInvalid = errors.New("pairing code is invalid")
	ErrPairingExpired = errors.New("pairing code has expired")
	ErrDeviceDenied   = errors.New("device token is invalid or revoked")
)

type Device struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Scopes      []string `json:"scopes"`
	CreatedAt   string   `json:"createdAt"`
	LastSeenAt  string   `json:"lastSeenAt,omitempty"`
	RevokedAt   string   `json:"revokedAt,omitempty"`
	PushEnabled bool     `json:"pushEnabled"`
}

type DevicePairing struct {
	ID        string    `json:"id"`
	Secret    string    `json:"secret"`
	Scopes    []string  `json:"scopes"`
	ExpiresAt time.Time `json:"expiresAt"`
}

type DevicePushTarget struct {
	DeviceID       string `json:"deviceId"`
	InstallationID string `json:"installationId"`
	Credential     string `json:"-"`
	Enabled        bool   `json:"enabled"`
	UpdatedAt      string `json:"updatedAt"`
}

func (r *Registry) CreateDevicePairing(name string, scopes []string, ttl time.Duration) (DevicePairing, error) {
	name = strings.TrimSpace(name)
	if len(name) > 120 {
		return DevicePairing{}, fmt.Errorf("device name must be at most 120 characters")
	}
	if ttl <= 0 || ttl > 15*time.Minute {
		return DevicePairing{}, fmt.Errorf("pairing lifetime must be between 1ns and 15m")
	}
	scopes, err := normalizeDeviceScopes(scopes)
	if err != nil {
		return DevicePairing{}, err
	}
	if err := r.pruneDevicePairings(time.Now().UTC()); err != nil {
		return DevicePairing{}, fmt.Errorf("prune device pairings: %w", err)
	}
	secret, err := randomDeviceSecret(32)
	if err != nil {
		return DevicePairing{}, err
	}
	pairing := DevicePairing{ID: uuid.NewString(), Secret: secret, Scopes: scopes, ExpiresAt: time.Now().UTC().Add(ttl)}
	_, err = r.db.Exec(`INSERT INTO device_pairings (id,secret_hash,requested_name,scopes,expires_at) VALUES (?,?,?,?,?)`, pairing.ID, hashDeviceSecret(secret), name, strings.Join(scopes, ","), pairing.ExpiresAt.Format(time.RFC3339Nano))
	if err != nil {
		return DevicePairing{}, fmt.Errorf("store device pairing: %w", err)
	}
	return pairing, nil
}

func (r *Registry) pruneDevicePairings(now time.Time) error {
	cutoff := now.Add(-24 * time.Hour).Format(time.RFC3339Nano)
	_, err := r.db.Exec(`DELETE FROM device_pairings
		WHERE (consumed_at<>'' AND datetime(consumed_at)<datetime(?))
		   OR (datetime(expires_at)<datetime(?) AND datetime(created_at)<datetime(?))`, cutoff, now.Format(time.RFC3339Nano), cutoff)
	return err
}

func (r *Registry) CompleteDevicePairing(secret, name string) (Device, string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return Device{}, "", ErrPairingInvalid
	}
	tx, err := r.db.Begin()
	if err != nil {
		return Device{}, "", err
	}
	defer tx.Rollback()
	var pairingID, requestedName, storedScopes, expiresAt, consumedAt string
	err = tx.QueryRow(`SELECT id,requested_name,scopes,expires_at,consumed_at FROM device_pairings WHERE secret_hash=?`, hashDeviceSecret(secret)).Scan(&pairingID, &requestedName, &storedScopes, &expiresAt, &consumedAt)
	if errors.Is(err, sql.ErrNoRows) || consumedAt != "" {
		return Device{}, "", ErrPairingInvalid
	}
	if err != nil {
		return Device{}, "", err
	}
	expires, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil || !time.Now().UTC().Before(expires) {
		return Device{}, "", ErrPairingExpired
	}
	name = strings.TrimSpace(name)
	if name == "" {
		name = requestedName
	}
	if name == "" {
		name = "Agenthail device"
	}
	if len(name) > 120 {
		return Device{}, "", fmt.Errorf("device name must be at most 120 characters")
	}
	scopes := splitDeviceScopes(storedScopes)
	tokenSecret, err := randomDeviceSecret(32)
	if err != nil {
		return Device{}, "", err
	}
	token := deviceTokenPrefix + tokenSecret
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.Exec(`UPDATE device_pairings SET consumed_at=? WHERE id=? AND consumed_at=''`, now, pairingID)
	if err != nil {
		return Device{}, "", err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return Device{}, "", ErrPairingInvalid
	}
	device := Device{ID: uuid.NewString(), Name: name, Scopes: scopes, CreatedAt: now}
	if _, err := tx.Exec(`INSERT INTO paired_devices (id,name,token_hash,scopes,created_at) VALUES (?,?,?,?,?)`, device.ID, device.Name, hashDeviceSecret(token), strings.Join(scopes, ","), now); err != nil {
		return Device{}, "", err
	}
	if err := tx.Commit(); err != nil {
		return Device{}, "", err
	}
	return device, token, nil
}

func (r *Registry) AuthenticateDevice(token, requiredScope string) (Device, error) {
	if !strings.HasPrefix(token, deviceTokenPrefix) {
		return Device{}, ErrDeviceDenied
	}
	var device Device
	var scopes string
	err := r.db.QueryRow(`SELECT id,name,scopes,created_at,last_seen_at,revoked_at FROM paired_devices WHERE token_hash=?`, hashDeviceSecret(token)).Scan(&device.ID, &device.Name, &scopes, &device.CreatedAt, &device.LastSeenAt, &device.RevokedAt)
	if errors.Is(err, sql.ErrNoRows) || device.RevokedAt != "" {
		return Device{}, ErrDeviceDenied
	}
	if err != nil {
		return Device{}, err
	}
	device.Scopes = splitDeviceScopes(scopes)
	if requiredScope != "" && !deviceHasScope(device.Scopes, requiredScope) {
		return Device{}, ErrDeviceDenied
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE paired_devices SET last_seen_at=? WHERE id=? AND revoked_at=''`, now, device.ID); err != nil {
		return Device{}, err
	}
	device.LastSeenAt = now
	return device, nil
}

func (r *Registry) ListDevices(includeRevoked bool) ([]Device, error) {
	query := `SELECT d.id,d.name,d.scopes,d.created_at,d.last_seen_at,d.revoked_at,
		EXISTS(SELECT 1 FROM device_push_targets p WHERE p.device_id=d.id AND p.enabled=1)
		FROM paired_devices d`
	if !includeRevoked {
		query += ` WHERE d.revoked_at=''`
	}
	query += ` ORDER BY d.created_at DESC,d.id DESC`
	rows, err := r.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	devices := []Device{}
	for rows.Next() {
		var device Device
		var scopes string
		var pushEnabled int
		if err := rows.Scan(&device.ID, &device.Name, &scopes, &device.CreatedAt, &device.LastSeenAt, &device.RevokedAt, &pushEnabled); err != nil {
			return nil, err
		}
		device.Scopes = splitDeviceScopes(scopes)
		device.PushEnabled = pushEnabled != 0
		devices = append(devices, device)
	}
	return devices, rows.Err()
}

func (r *Registry) RevokeDevice(id string) error {
	result, err := r.db.Exec(`UPDATE paired_devices SET revoked_at=? WHERE id=? AND revoked_at=''`, time.Now().UTC().Format(time.RFC3339Nano), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed != 1 {
		return fmt.Errorf("active device not found")
	}
	return nil
}

func (r *Registry) SaveDevicePushTarget(deviceID, installationID, credential string) error {
	deviceID = strings.TrimSpace(deviceID)
	installationID = strings.TrimSpace(installationID)
	credential = strings.TrimSpace(credential)
	if deviceID == "" || installationID == "" || credential == "" {
		return fmt.Errorf("device id, installation id, and credential are required")
	}
	if len(installationID) > 160 || len(credential) > 256 {
		return fmt.Errorf("push registration is too large")
	}
	result, err := r.db.Exec(`INSERT INTO device_push_targets (device_id,installation_id,credential,enabled,updated_at)
		SELECT id,?,?,1,? FROM paired_devices WHERE id=? AND revoked_at=''
		ON CONFLICT(device_id) DO UPDATE SET installation_id=excluded.installation_id,credential=excluded.credential,enabled=1,updated_at=excluded.updated_at`, installationID, credential, time.Now().UTC().Format(time.RFC3339Nano), deviceID)
	if err != nil {
		return err
	}
	if changed, _ := result.RowsAffected(); changed == 0 {
		return fmt.Errorf("active device not found")
	}
	return nil
}

func (r *Registry) RemoveDevicePushTarget(deviceID string) error {
	_, err := r.db.Exec(`DELETE FROM device_push_targets WHERE device_id=?`, strings.TrimSpace(deviceID))
	return err
}

func (r *Registry) DevicePushTargets() ([]DevicePushTarget, error) {
	rows, err := r.db.Query(`SELECT p.device_id,p.installation_id,p.credential,p.enabled,p.updated_at
		FROM device_push_targets p JOIN paired_devices d ON d.id=p.device_id
		WHERE p.enabled=1 AND d.revoked_at='' ORDER BY p.updated_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := []DevicePushTarget{}
	for rows.Next() {
		var target DevicePushTarget
		var enabled int
		if err := rows.Scan(&target.DeviceID, &target.InstallationID, &target.Credential, &enabled, &target.UpdatedAt); err != nil {
			return nil, err
		}
		target.Enabled = enabled != 0
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func randomDeviceSecret(size int) (string, error) {
	raw := make([]byte, size)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate device secret: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func hashDeviceSecret(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func normalizeDeviceScopes(scopes []string) ([]string, error) {
	if len(scopes) == 0 {
		scopes = []string{"read", "control"}
	}
	set := map[string]bool{}
	for _, scope := range scopes {
		scope = strings.TrimSpace(scope)
		if scope != "read" && scope != "control" && scope != "settings" {
			return nil, fmt.Errorf("unsupported device scope %q", scope)
		}
		set[scope] = true
	}
	result := make([]string, 0, len(set))
	for scope := range set {
		result = append(result, scope)
	}
	sort.Strings(result)
	return result, nil
}

func splitDeviceScopes(value string) []string {
	if value == "" {
		return []string{}
	}
	return strings.Split(value, ",")
}

func deviceHasScope(scopes []string, required string) bool {
	for _, scope := range scopes {
		if scope == required || scope == "settings" {
			return true
		}
	}
	return false
}

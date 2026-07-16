package registry

import (
	"errors"
	"strings"
	"testing"
	"time"
)

func TestDevicePairingIsScopedSingleUseAndRevocable(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Zain's iPhone", []string{"control", "read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "Zain's iPhone" || strings.Join(device.Scopes, ",") != "control,read" || !strings.HasPrefix(token, deviceTokenPrefix) {
		t.Fatalf("device=%+v token=%q", device, token)
	}
	if _, _, err := r.CompleteDevicePairing(pairing.Secret, "Again"); !errors.Is(err, ErrPairingInvalid) {
		t.Fatalf("second pairing err=%v", err)
	}
	if _, err := r.AuthenticateDevice(token, "read"); err != nil {
		t.Fatalf("authenticate read: %v", err)
	}
	if _, err := r.AuthenticateDevice(token, "settings"); !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("settings scope err=%v", err)
	}
	if err := r.RevokeDevice(device.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AuthenticateDevice(token, "read"); !errors.Is(err, ErrDeviceDenied) {
		t.Fatalf("revoked token err=%v", err)
	}
}

func TestDevicePairingExpiresAndNeverStoresPlaintextSecrets(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Phone", nil, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, _, err := r.CompleteDevicePairing(pairing.Secret, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired pairing err=%v", err)
	}
	var stored string
	if err := r.db.QueryRow(`SELECT secret_hash FROM device_pairings WHERE id=?`, pairing.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == pairing.Secret || len(stored) != 64 {
		t.Fatalf("stored pairing secret=%q", stored)
	}
}

func TestDevicePushTargetFollowsDeviceLifecycle(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Phone", nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := r.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	devices, err := r.ListDevices(false)
	if err != nil || len(devices) != 1 || !devices[0].PushEnabled {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
	targets, err := r.DevicePushTargets()
	if err != nil || len(targets) != 1 || targets[0].Credential != "credential" {
		t.Fatalf("targets=%+v err=%v", targets, err)
	}
	if err := r.RevokeDevice(device.ID); err != nil {
		t.Fatal(err)
	}
	targets, err = r.DevicePushTargets()
	if err != nil || len(targets) != 0 {
		t.Fatalf("revoked targets=%+v err=%v", targets, err)
	}
}

func TestCreateDevicePairingPrunesOldPairings(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now().UTC()
	old := now.Add(-48 * time.Hour).Format(time.RFC3339Nano)
	recent := now.Add(-time.Hour).Format(time.RFC3339Nano)
	activeExpiry := now.Add(time.Minute).Format(time.RFC3339Nano)
	for _, row := range []struct {
		id, expires, created, consumed string
	}{
		{id: "expired-old", expires: old, created: old},
		{id: "consumed-old", expires: activeExpiry, created: old, consumed: old},
		{id: "expired-recent", expires: recent, created: recent},
		{id: "active", expires: activeExpiry, created: recent},
	} {
		if _, err := r.db.Exec(`INSERT INTO device_pairings (id,secret_hash,scopes,expires_at,created_at,consumed_at) VALUES (?,?,?,?,?,?)`, row.id, row.id, "read", row.expires, row.created, row.consumed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.CreateDevicePairing("New", nil, time.Minute); err != nil {
		t.Fatal(err)
	}
	rows, err := r.db.Query(`SELECT id FROM device_pairings WHERE id IN ('expired-old','consumed-old','expired-recent','active') ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if strings.Join(ids, ",") != "active,expired-recent" {
		t.Fatalf("retained=%v", ids)
	}
}

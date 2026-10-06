package registry

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDevicePairingIsScopedSingleUseAndRevocable(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Test iPhone", []string{"control", "read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "Test iPhone" || strings.Join(device.Scopes, ",") != "control,read" || !strings.HasPrefix(token, deviceTokenPrefix) {
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
	dir := t.TempDir()
	r, err := Open(filepath.Join(dir, "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	expired, err := r.CreateDevicePairing("Phone", nil, time.Nanosecond)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Millisecond)
	if _, _, err := r.CompleteDevicePairing(expired.Secret, ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired pairing err=%v", err)
	}
	pairing, err := r.CreateDevicePairing("Tablet", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(dir, "registry.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("files=%v err=%v", files, err)
	}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{expired.Secret, pairing.Secret, token, strings.TrimPrefix(token, deviceTokenPrefix)} {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatalf("%s stores a plaintext pairing secret or device token", filepath.Base(file))
			}
		}
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

func TestDeviceAuthenticationRecordsLastSeenAtMostOncePerMinute(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Test iPhone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, "")
	if err != nil {
		t.Fatal(err)
	}
	first, err := r.AuthenticateDevice(token, "read")
	if err != nil || first.LastSeenAt == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if _, err := r.db.Exec(`CREATE TRIGGER reject_device_touch BEFORE UPDATE ON paired_devices BEGIN SELECT RAISE(ABORT, 'unexpected last-seen write'); END`); err != nil {
		t.Fatal(err)
	}
	repeat, err := r.AuthenticateDevice(token, "read")
	if err != nil || repeat.LastSeenAt != first.LastSeenAt {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}
	if _, err := r.db.Exec(`DROP TRIGGER reject_device_touch`); err != nil {
		t.Fatal(err)
	}
	stale := time.Now().UTC().Add(-2 * deviceLastSeenResolution).Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE paired_devices SET last_seen_at=?`, stale); err != nil {
		t.Fatal(err)
	}
	refreshed, err := r.AuthenticateDevice(token, "read")
	if err != nil || refreshed.LastSeenAt == stale {
		t.Fatalf("refreshed=%+v err=%v", refreshed, err)
	}
}

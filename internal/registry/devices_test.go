package registry

import (
	"bytes"
	"context"
	"database/sql"
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
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if device.Name != "Test iPhone" || strings.Join(device.Scopes, ",") != "control,read" || !strings.HasPrefix(token, deviceTokenPrefix) {
		t.Fatalf("device=%+v token=%q", device, token)
	}
	if _, _, err := r.CompleteDevicePairing(pairing.Secret, "Again", ""); !errors.Is(err, ErrPairingInvalid) {
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
	if _, _, err := r.CompleteDevicePairing(expired.Secret, "", ""); !errors.Is(err, ErrPairingExpired) {
		t.Fatalf("expired pairing err=%v", err)
	}
	pairing, err := r.CreateDevicePairing("Tablet", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
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
	device, _, err := r.CompleteDevicePairing(pairing.Secret, "", "")
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
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE TABLE touches(at TEXT); CREATE TRIGGER count_touch AFTER UPDATE OF last_seen_at ON paired_devices BEGIN INSERT INTO touches VALUES (NEW.last_seen_at); END`); err != nil {
		t.Fatal(err)
	}
	stored := func() string {
		t.Helper()
		var seen string
		if err := r.read.QueryRow(`SELECT last_seen_at FROM paired_devices WHERE id=?`, device.ID).Scan(&seen); err != nil {
			t.Fatal(err)
		}
		return seen
	}
	touches := func() int {
		t.Helper()
		var count int
		if err := r.read.QueryRow(`SELECT count(*) FROM touches`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	first, err := r.AuthenticateDevice(token, "read")
	if err != nil || first.LastSeenAt == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	awaitCondition(t, "the first stamp to be written", func() bool { return stored() == first.LastSeenAt })
	repeat, err := r.AuthenticateDevice(token, "read")
	if err != nil || repeat.LastSeenAt != first.LastSeenAt {
		t.Fatalf("repeat=%+v err=%v", repeat, err)
	}
	r.seen.writes.Wait()
	if count := touches(); count != 1 {
		t.Fatalf("a request within a minute wrote last-seen again: %d writes", count)
	}
	stale := time.Now().UTC().Add(-2 * deviceLastSeenResolution).Format(time.RFC3339Nano)
	if _, err := r.db.Exec(`UPDATE paired_devices SET last_seen_at=?`, stale); err != nil {
		t.Fatal(err)
	}
	refreshed, err := r.AuthenticateDevice(token, "read")
	if err != nil || refreshed.LastSeenAt == stale {
		t.Fatalf("refreshed=%+v err=%v", refreshed, err)
	}
	awaitCondition(t, "the refreshed stamp to be written", func() bool { return stored() == refreshed.LastSeenAt })
}

// A loaded Mac had other processes holding the registry's write lock while a
// phone's notification service read a request; authenticating it must not
// wait on that lock.
func TestDeviceAuthenticationDoesNotWaitForTheWriteLock(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("Test iPhone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", r.path+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	holder, err := other.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	if _, err := holder.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	if _, err := r.AuthenticateDevice(token, "read"); err != nil {
		t.Fatal(err)
	}
	if took := time.Since(started); took > time.Second {
		t.Fatalf("authentication waited %s for another process's write", took)
	}
	if _, err := holder.ExecContext(context.Background(), `COMMIT`); err != nil {
		t.Fatal(err)
	}
	awaitCondition(t, "the stamp to be written once the lock is free", func() bool {
		var seen string
		return r.read.QueryRow(`SELECT last_seen_at FROM paired_devices WHERE id=?`, device.ID).Scan(&seen) == nil && seen != ""
	})
}

// A stamp whose write fails, as when another process holds the write lock
// past the busy timeout, is kept and written before the registry closes,
// without another request.
func TestAFailedLastSeenStampIsWrittenBeforeTheRegistryCloses(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	pairing, err := r.CreateDevicePairing("Test iPhone", []string{"read"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, token, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`CREATE TRIGGER refuse_touch BEFORE UPDATE OF last_seen_at ON paired_devices BEGIN SELECT RAISE(ABORT, 'busy'); END`); err != nil {
		t.Fatal(err)
	}
	seen, err := r.AuthenticateDevice(token, "read")
	if err != nil {
		t.Fatal(err)
	}
	r.seen.writes.Wait()
	if _, err := r.db.Exec(`DROP TRIGGER refuse_touch`); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	reopened := openRegistryAt(t, path)
	var stored string
	if err := reopened.read.QueryRow(`SELECT last_seen_at FROM paired_devices WHERE id=?`, device.ID).Scan(&stored); err != nil || stored != seen.LastSeenAt {
		t.Fatalf("stored=%q err=%v, want the stamp %q that failed to write", stored, err, seen.LastSeenAt)
	}
}

func openRegistryAt(t *testing.T, path string) *Registry {
	t.Helper()
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestAHostTokenBelongsToTheHostThatPaired(t *testing.T) {
	r := openTestRegistry(t)
	hostPairing, err := r.CreateDevicePairing("vps", []string{"read", "control", HostScope}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.CompleteDevicePairing(hostPairing.Secret, "", "not-a-host"); err == nil {
		t.Fatal("a malformed host id was accepted")
	}
	hostPairing, err = r.CreateDevicePairing("vps", []string{"read", "control", HostScope}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, hostToken, err := r.CompleteDevicePairing(hostPairing.Secret, "", "h0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	if device, err := r.AuthenticateDevice(hostToken, "control"); err != nil || device.HostID != "h0123456789ab" {
		t.Fatalf("host device=%+v err=%v", device, err)
	}
	phonePairing, err := r.CreateDevicePairing("phone", []string{"read", "control"}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	_, phoneToken, err := r.CompleteDevicePairing(phonePairing.Secret, "", "h0123456789ab")
	if err != nil {
		t.Fatal(err)
	}
	if device, err := r.AuthenticateDevice(phoneToken, "control"); err != nil || device.HostID != "" {
		t.Fatalf("a device without the host scope took a host id: %+v err=%v", device, err)
	}
	devices, err := r.ListDevices(false)
	if err != nil || len(devices) != 2 {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
	for _, device := range devices {
		if device.IsHost() != (device.HostID == "h0123456789ab") {
			t.Fatalf("listed device %+v", device)
		}
	}
}

func TestPairingStatusNamesTheDeviceItsCodePaired(t *testing.T) {
	r := openTestRegistry(t)
	paired := map[string]string{}
	for _, name := range []string{"First iPhone", "Second iPhone"} {
		pairing, err := r.CreateDevicePairing(name, []string{"read"}, time.Minute)
		if err != nil {
			t.Fatal(err)
		}
		device, _, err := r.CompleteDevicePairing(pairing.Secret, "", "")
		if err != nil {
			t.Fatal(err)
		}
		paired[pairing.ID] = device.ID
	}
	const sameInstant = "2026-10-22T10:00:00Z"
	if _, err := r.db.Exec(`UPDATE device_pairings SET consumed_at=?; UPDATE paired_devices SET created_at=?`, sameInstant, sameInstant); err != nil {
		t.Fatal(err)
	}
	for pairingID, deviceID := range paired {
		status, err := r.PairingStatus(pairingID)
		if err != nil {
			t.Fatal(err)
		}
		if status.State != PairingPaired || status.Device == nil || status.Device.ID != deviceID {
			t.Fatalf("pairing %s status=%+v, want device %s", pairingID, status, deviceID)
		}
	}
}

func awaitCondition(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

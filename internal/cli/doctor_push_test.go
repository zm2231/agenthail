package cli

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

func TestDoctorShowsEachDevicesLastPushAndFailsOnAFailureSinceTheLastDelivery(t *testing.T) {
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "https://relay.example.test")
	reg := doctorPushRegistry(t)
	now := time.Date(2026, 11, 2, 23, 30, 0, 0, time.UTC)
	health, err := pushHealthOf(reg, now)
	if err != nil || len(health.Devices) != 0 || len(health.Problems) != 0 || !strings.Contains(health.String(), "no paired device takes notifications") {
		t.Fatalf("health=%+v err=%v", health, err)
	}

	device := pairPushDevice(t, reg, "iPhone")
	health, _ = pushHealthOf(reg, now)
	if len(health.Devices) != 1 || len(health.Problems) != 0 || !strings.Contains(health.String(), "no push delivered yet") {
		t.Fatalf("a device never pushed to: %+v\n%s", health, health)
	}

	delivered := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	if err := reg.RecordPushOutcome(device, "session.waiting", delivered, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordPushOutcome(device, "turn.completed", delivered.Add(time.Minute), errors.New("relay returned 502: apns_rejected")); err != nil {
		t.Fatal(err)
	}
	health, _ = pushHealthOf(reg, now)
	if len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "the last push to iPhone failed") || !strings.Contains(health.Problems[0], "apns_rejected") {
		t.Fatalf("a failure after the last delivery was not a problem: %+v", health)
	}
	if text := health.String(); !strings.Contains(text, "last delivered 2026-11-02T23:00:00") || !strings.Contains(text, "last error at 2026-11-02T23:01:00") || !strings.Contains(text, "arrival is not display") {
		t.Fatalf("doctor text=%s", text)
	}

	if err := reg.RecordPushOutcome(device, "session.waiting", delivered.Add(2*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	health, _ = pushHealthOf(reg, now)
	if len(health.Problems) != 0 || health.Devices[0].Error == "" {
		t.Fatalf("a delivery after the failure should clear the problem but keep the error on record: %+v", health)
	}

	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	if health, _ = pushHealthOf(reg, now); health.RelayConfigured {
		t.Skip("this build bundles a relay URL")
	}
	if len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "no push relay is configured") {
		t.Fatalf("paired devices without a relay: %+v", health)
	}
}

func TestDoctorFailsWhenAcceptedPushesNeverReachThePhone(t *testing.T) {
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "https://relay.example.test")
	reg := doctorPushRegistry(t)
	device := pairPushDevice(t, reg, "iPhone")
	sent := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	for i, id := range []string{"p1", "p2", "p3"} {
		if err := reg.RecordPushSent(device, id, "session.waiting", sent.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
		if err := reg.SettlePushSent(id, true, sent); err != nil {
			t.Fatal(err)
		}
	}
	if arrived, err := reg.RecordPushReceipt(device, "p1", sent.Add(5*time.Second), "on"); err != nil || !arrived {
		t.Fatalf("receipt arrived=%v err=%v", arrived, err)
	}
	if arrived, _ := reg.RecordPushReceipt("someone-else", "p2", sent, "on"); arrived {
		t.Fatal("another device's receipt counted")
	}

	health, _ := pushHealthOf(reg, sent.Add(5*time.Minute))
	if len(health.Problems) != 0 || health.Devices[0].Unarrived != 0 {
		t.Fatalf("pushes still inside the grace period were counted as lost: %+v", health)
	}
	health, _ = pushHealthOf(reg, sent.Add(time.Hour))
	if health.Devices[0].Unarrived != 2 || len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "APNs accepted 2 pushes to iPhone that never reached the phone") {
		t.Fatalf("lost pushes: %+v", health)
	}
	if text := health.String(); !strings.Contains(text, "last arrived on the phone 2026-11-02T23:00:05Z (alerts on)") {
		t.Fatalf("doctor text=%s", text)
	}

	if arrived, _ := reg.RecordPushReceipt(device, "p3", sent.Add(2*time.Minute+time.Second), "off"); !arrived {
		t.Fatal("a receipt for a later push did not count")
	}
	health, _ = pushHealthOf(reg, sent.Add(time.Hour))
	if len(health.Problems) != 0 || health.Devices[0].Alerts != "off" {
		t.Fatalf("an arrival after the lost pushes should clear the problem and carry the alert setting: %+v", health)
	}
	if arrived, _ := reg.RecordPushReceipt(device, "p1", sent.Add(time.Hour), "on"); arrived {
		t.Fatal("a repeated receipt counted twice")
	}
}

func TestDoctorExpectsNoReceiptsFromARelayWithoutThem(t *testing.T) {
	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "https://relay.example.test")
	reg := doctorPushRegistry(t)
	device := pairPushDevice(t, reg, "iPhone")
	sent := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	if err := reg.RecordPushSent(device, "unconfirmed", "session.waiting", sent); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		if err := reg.RecordPushOutcome(device, "session.waiting", sent.Add(time.Duration(i)*time.Minute), nil); err != nil {
			t.Fatal(err)
		}
	}
	if health, _ := pushHealthOf(reg, sent.Add(time.Hour)); len(health.Problems) != 0 || health.Devices[0].Unarrived != 0 {
		t.Fatalf("pushes an older relay accepted without a receipt were counted as lost: %+v", health)
	}
}

func doctorPushRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	return reg
}

func pairPushDevice(t *testing.T, reg *registry.Registry, name string) string {
	t.Helper()
	pairing, err := reg.CreateDevicePairing(name, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := reg.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := reg.SaveDevicePushTarget(device.ID, "installation", "credential"); err != nil {
		t.Fatal(err)
	}
	return device.ID
}

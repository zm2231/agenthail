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
	reg, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { reg.Close() })
	health, err := pushHealthOf(reg)
	if err != nil || len(health.Devices) != 0 || len(health.Problems) != 0 || !strings.Contains(health.String(), "no paired device takes notifications") {
		t.Fatalf("health=%+v err=%v", health, err)
	}

	pairing, err := reg.CreateDevicePairing("iPhone", nil, time.Minute)
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
	health, _ = pushHealthOf(reg)
	if len(health.Devices) != 1 || len(health.Problems) != 0 || !strings.Contains(health.String(), "no push delivered yet") {
		t.Fatalf("a device never pushed to: %+v\n%s", health, health)
	}

	delivered := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	if err := reg.RecordPushOutcome(device.ID, "session.waiting", delivered, nil); err != nil {
		t.Fatal(err)
	}
	if err := reg.RecordPushOutcome(device.ID, "turn.completed", delivered.Add(time.Minute), errors.New("relay returned 502: apns_rejected")); err != nil {
		t.Fatal(err)
	}
	health, _ = pushHealthOf(reg)
	if len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "the last push to iPhone failed") || !strings.Contains(health.Problems[0], "apns_rejected") {
		t.Fatalf("a failure after the last delivery was not a problem: %+v", health)
	}
	if text := health.String(); !strings.Contains(text, "last delivered 2026-11-02T23:00:00") || !strings.Contains(text, "last error at 2026-11-02T23:01:00") {
		t.Fatalf("doctor text=%s", text)
	}

	if err := reg.RecordPushOutcome(device.ID, "session.waiting", delivered.Add(2*time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	health, _ = pushHealthOf(reg)
	if len(health.Problems) != 0 || health.Devices[0].Error == "" {
		t.Fatalf("a delivery after the failure should clear the problem but keep the error on record: %+v", health)
	}

	t.Setenv("AGENTHAIL_PUSH_RELAY_URL", "")
	if health, _ = pushHealthOf(reg); health.RelayConfigured {
		t.Skip("this build bundles a relay URL")
	}
	if len(health.Problems) != 1 || !strings.Contains(health.Problems[0], "no push relay is configured") {
		t.Fatalf("paired devices without a relay: %+v", health)
	}
}

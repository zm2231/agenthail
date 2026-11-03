package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestDoctorShowsEachPhonesConnectionDropsAsInformation(t *testing.T) {
	reg := doctorPushRegistry(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	health, err := connectionHealthOf(reg, now)
	if err != nil || len(health.Devices) != 0 || !strings.Contains(health.String(), "no phone has reported a dropped connection") {
		t.Fatalf("health=%+v err=%v", health, err)
	}

	self, err := reg.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	peer, err := reg.AddHost(registry.PairedHost{ID: "h00000000bbbb", Name: "build-box", Endpoint: "https://build-box.example.net", Address: "192.0.2.10", Token: "token"})
	if err != nil {
		t.Fatal(err)
	}
	device := pairPushDevice(t, reg, "iPhone")
	elapsed, foreground := int64(15000), int64(3000)
	drops := []registry.ConnectionDrop{
		{ID: "old00001", Host: self.ID, Path: "connect", StartedAt: now.Add(-48 * time.Hour), RecoveredAt: now.Add(-47 * time.Hour), FirstError: "URLError.-1004", LastError: "URLError.-1004", Attempts: 1, Shown: "offline"},
		{ID: "held0001", Host: self.ID, Path: "stream", StartedAt: now.Add(-2 * time.Hour), RecoveredAt: now.Add(-2*time.Hour + 2*time.Second), RecoveredBy: "stream probe", FirstError: "streamClosed", LastError: "streamClosed", Attempts: 1, Shown: "held"},
		{ID: "relay001", Host: peer.ID, Via: self.ID, Path: "request", StartedAt: now.Add(-time.Hour), RecoveredAt: now.Add(-time.Hour + time.Minute), RecoveredBy: "peer connected", FirstError: "host_unreachable", LastError: "host_unreachable", Attempts: 3, Shown: "offline"},
		{ID: "last0001", Host: self.ID, Path: "stream probe", StartedAt: now.Add(-10 * time.Minute), RecoveredAt: now.Add(-10*time.Minute + 4*time.Second), RecoveredBy: "snapshot", FirstError: "URLError.-1001", LastError: "URLError.-1001", ElapsedMs: &elapsed, Attempts: 2, Shown: "held", SinceForegroundMs: &foreground, Network: "wifi", VPN: true},
	}
	if _, err := reg.RecordConnectionDrops(device, drops); err != nil {
		t.Fatal(err)
	}
	health, err = connectionHealthOf(reg, now)
	if err != nil || len(health.Devices) != 1 || len(health.Warnings) != 0 {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	got := health.Devices[0]
	if got.Recent != 3 || got.Held != 2 || got.Offline != 1 || got.Relayed != 1 {
		t.Fatalf("the last day's drops were miscounted: %+v", got)
	}
	text := health.String()
	for _, want := range []string{"information, not a failure", "iPhone", "3 drops in 24 h (2 held, 1 shown offline, 1 through another computer)",
		"stream probe to this computer directly", "URLError.-1001 after 15.0 s", "on wifi via vpn", "3 s after returning to the app", "back after 4s by snapshot"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor text lacks %q:\n%s", want, text)
		}
	}

	var lost []registry.ConnectionDrop
	for i := range connectionSteadyLosses {
		lost = append(lost, registry.ConnectionDrop{ID: fmt.Sprintf("lost%04d", i), Host: peer.ID, Path: "connect", StartedAt: now.Add(time.Duration(i-5) * time.Minute),
			RecoveredAt: now.Add(time.Duration(i-4) * time.Minute), FirstError: "URLError.-1001", LastError: "URLError.-1001", Attempts: 1, Shown: "offline"})
	}
	if _, err := reg.RecordConnectionDrops(device, lost); err != nil {
		t.Fatal(err)
	}
	health, _ = connectionHealthOf(reg, now)
	if len(health.Warnings) != 1 || !strings.Contains(health.Warnings[0], "iPhone showed a computer offline 3 times in a row while in use") {
		t.Fatalf("steady-use losses were not a warning: %+v", health.Warnings)
	}
	if !strings.Contains(health.String(), "connect to build-box directly") {
		t.Fatalf("the last drop does not name its computer:\n%s", health)
	}

	resumed := lost[len(lost)-1]
	resumed.ID, resumed.SinceForegroundMs, resumed.StartedAt = "resume01", &foreground, now.Add(-time.Minute)
	if _, err := reg.RecordConnectionDrops(device, []registry.ConnectionDrop{resumed}); err != nil {
		t.Fatal(err)
	}
	if health, _ = connectionHealthOf(reg, now); len(health.Warnings) != 0 {
		t.Fatalf("a loss right after returning to the app counted as steady use: %+v", health.Warnings)
	}
}

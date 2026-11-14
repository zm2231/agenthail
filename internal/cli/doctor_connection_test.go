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

func TestDoctorNamesTheCauseOfEachStreamDropFromTheDaemonsSideOfTheStream(t *testing.T) {
	reg := doctorPushRegistry(t)
	now := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	self, err := reg.HostIdentity()
	if err != nil {
		t.Fatal(err)
	}
	device := pairPushDevice(t, reg, "iPhone")
	at := func(minutes int) time.Time { return now.Add(time.Duration(-minutes) * time.Minute) }
	drop := func(id string, started time.Time, network, failure string) registry.ConnectionDrop {
		return registry.ConnectionDrop{ID: id, Host: self.ID, Path: "stream", StartedAt: started, RecoveredAt: started.Add(2 * time.Second), RecoveredBy: "stream probe",
			FirstError: failure, LastError: failure, Attempts: 1, Shown: "held", Network: network, VPN: true}
	}
	relayed := drop("relay001", at(10), "wifi", "URLError.-1001")
	relayed.Via, relayed.Host = self.ID, "h00000000bbbb"
	drops := []registry.ConnectionDrop{
		drop("quiet001", at(60), "wifi", "URLError.-1001"),
		drop("trans001", at(50), "wifi", "URLError.-1001"),
		drop("trans002", at(45), "wifi", "URLError.-1001"),
		drop("half0001", at(40), "cellular", "URLError.-1001"),
		drop("reset001", at(30), "cellular", "URLError.-1005"),
		drop("stop0001", at(25), "cellular", "URLError.-1005"),
		drop("none0001", at(20), "", "URLError.-1001"),
		relayed,
	}
	if _, err := reg.RecordConnectionDrops(device, drops); err != nil {
		t.Fatal(err)
	}
	streams := []registry.DeviceStream{
		{OpenedAt: at(70), ClosedAt: at(60).Add(time.Second), LastFlushAt: at(60).Add(-40 * time.Second), Ended: registry.StreamEndedClientClosed, TailnetPath: "direct"},
		{OpenedAt: at(55), ClosedAt: at(50).Add(2 * time.Second), LastFlushAt: at(50).Add(-5 * time.Second), Ended: registry.StreamEndedClientClosed, TailnetPath: "direct"},
		{OpenedAt: at(49), ClosedAt: at(45).Add(-time.Second), LastFlushAt: at(45).Add(-10 * time.Second), Ended: registry.StreamEndedClientClosed, TailnetPath: "direct"},
		{OpenedAt: at(44), ClosedAt: at(40).Add(70 * time.Second), LastFlushAt: at(40).Add(45 * time.Second), Ended: registry.StreamEndedWriteDeadline, TailnetPath: "relay:nyc"},
		{OpenedAt: at(39), ClosedAt: at(30).Add(-time.Second), LastFlushAt: at(30).Add(-8 * time.Second), Ended: registry.StreamEndedWriteFailed, TailnetPath: "relay:nyc"},
		{OpenedAt: at(29), ClosedAt: at(25).Add(-2 * time.Second), LastFlushAt: at(25).Add(-3 * time.Second), Ended: registry.StreamEndedDaemonStopped, TailnetPath: "direct"},
	}
	for _, stream := range streams {
		stream.Route = "events"
		if err := reg.RecordDeviceStream(device, stream); err != nil {
			t.Fatal(err)
		}
	}
	session := registry.DeviceStream{Route: "session-stream", OpenedAt: at(21), ClosedAt: at(19), LastFlushAt: at(20), Ended: registry.StreamEndedClientClosed, TailnetPath: "direct"}
	if err := reg.RecordDeviceStream(device, session); err != nil {
		t.Fatal(err)
	}

	health, err := connectionHealthOf(reg, now)
	if err != nil || len(health.Devices) != 1 {
		t.Fatalf("health=%+v err=%v", health, err)
	}
	want := []dropCauseCount{
		{Cause: dropCauseTransit, Network: "wifi", TailnetPath: "direct", Count: 2},
		{Cause: dropCauseStopped, Network: "cellular", TailnetPath: "direct", Count: 1},
		{Cause: dropCauseQuiet, Network: "wifi", TailnetPath: "direct", Count: 1},
		{Cause: dropCauseHalfOpen, Network: "cellular", TailnetPath: "relay:nyc", Count: 1},
		{Cause: dropCauseNoRecord, Network: dropNetworkUnknown, Count: 1},
		{Cause: dropCauseReset, Network: "cellular", TailnetPath: "relay:nyc", Count: 1},
	}
	if got := health.Devices[0].Causes; fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("causes=%+v\nwant   %+v", got, want)
	}
	if len(health.Warnings) != 1 || !strings.Contains(health.Warnings[0], "1 of iPhone's stream drops came after this computer sent it nothing for over 25 s; that is an Agenthail bug") {
		t.Fatalf("a quiet daemon was not a warning: %+v", health.Warnings)
	}
	text := health.String()
	for _, want := range []string{"2 lost in transit on wifi (direct)", "1 half-open on cellular (relay nyc)", "1 no daemon record on unknown network,"} {
		if !strings.Contains(text, want) {
			t.Fatalf("doctor text lacks %q:\n%s", want, text)
		}
	}
}

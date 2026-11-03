package registry

import (
	"fmt"
	"testing"
	"time"
)

func pairNamedDevice(t *testing.T, r *Registry, name string) string {
	t.Helper()
	pairing, err := r.CreateDevicePairing(name, nil, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := r.CompleteDevicePairing(pairing.Secret, "", "")
	if err != nil {
		t.Fatal(err)
	}
	return device.ID
}

func testDrop(id string, started time.Time) ConnectionDrop {
	elapsed := int64(15000)
	return ConnectionDrop{ID: id, Host: "studio", Path: "stream probe", StartedAt: started, RecoveredAt: started.Add(4 * time.Second), RecoveredBy: "snapshot",
		FirstError: "URLError.-1001", LastError: "URLError.-1001", ElapsedMs: &elapsed, Attempts: 2, Shown: "held", Network: "wifi", VPN: true}
}

func TestConnectionDropsAreKeptOncePerIDNewestFirstAndBoundedPerDevice(t *testing.T) {
	r := openTestRegistry(t)
	phone := pairNamedDevice(t, r, "iPhone")
	tablet := pairNamedDevice(t, r, "iPad")
	start := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)

	recorded, err := r.RecordConnectionDrops(phone, []ConnectionDrop{testDrop("drop0001", start), testDrop("drop0002", start.Add(time.Minute))})
	if err != nil || recorded != 2 {
		t.Fatalf("recorded=%d err=%v", recorded, err)
	}
	if recorded, _ = r.RecordConnectionDrops(phone, []ConnectionDrop{testDrop("drop0001", start)}); recorded != 0 {
		t.Fatalf("a repeated upload recorded %d drops again", recorded)
	}
	if recorded, _ = r.RecordConnectionDrops(tablet, []ConnectionDrop{testDrop("drop0001", start)}); recorded != 1 {
		t.Fatal("another device's drop with the same id was refused")
	}

	histories, err := r.ConnectionDropHistories()
	if err != nil || len(histories) != 2 {
		t.Fatalf("histories=%+v err=%v", histories, err)
	}
	got := histories[1]
	if got.Name != "iPhone" || len(got.Drops) != 2 || got.Drops[0].ID != "drop0002" {
		t.Fatalf("the phone's drops are not newest first: %+v", got)
	}
	first := got.Drops[1]
	if *first.ElapsedMs != 15000 || first.SinceForegroundMs != nil || !first.VPN || first.Shown != "held" || !first.RecoveredAt.Equal(start.Add(4*time.Second)) {
		t.Fatalf("a drop did not round-trip: %+v", first)
	}

	var many []ConnectionDrop
	for i := range connectionDropsKept + 5 {
		many = append(many, testDrop(fmt.Sprintf("bulk%04d", i), start.Add(time.Duration(i+2)*time.Minute)))
	}
	if _, err := r.RecordConnectionDrops(phone, many); err != nil {
		t.Fatal(err)
	}
	histories, _ = r.ConnectionDropHistories()
	if kept := histories[1].Drops; len(kept) != connectionDropsKept || kept[0].ID != fmt.Sprintf("bulk%04d", connectionDropsKept+4) {
		t.Fatalf("kept %d drops, newest %s", len(kept), kept[0].ID)
	}
	if len(histories[0].Drops) != 1 {
		t.Fatal("bounding the phone's drops removed another device's")
	}

	if err := r.RevokeDevice(phone); err != nil {
		t.Fatal(err)
	}
	if recorded, _ = r.RecordConnectionDrops(phone, []ConnectionDrop{testDrop("late0001", start)}); recorded != 0 {
		t.Fatal("a revoked device recorded a drop")
	}
	if histories, _ = r.ConnectionDropHistories(); len(histories) != 1 || histories[0].Name != "iPad" {
		t.Fatalf("a revoked device's drops are still shown: %+v", histories)
	}
}

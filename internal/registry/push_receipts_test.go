package registry

import (
	"fmt"
	"testing"
	"time"
)

func TestWithdrawnSendsNeverEvictConfirmedPushHistory(t *testing.T) {
	r := openTestRegistry(t)
	pairing, err := r.CreateDevicePairing("iPhone", nil, time.Minute)
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
	base := time.Date(2026, 11, 2, 22, 0, 0, 0, time.UTC)
	for i := range pushSendsKept {
		id := fmt.Sprintf("confirmed%03d", i)
		if err := r.RecordPushSent(device.ID, id, "session.waiting", base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
		if err := r.SettlePushSent(id, true, base.Add(time.Duration(i)*time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	unarrived := func() int {
		statuses, err := r.PushStatuses(base.Add(time.Hour))
		if err != nil || len(statuses) != 1 {
			t.Fatalf("statuses=%+v err=%v", statuses, err)
		}
		return statuses[0].Unarrived
	}
	if got := unarrived(); got != pushSendsKept {
		t.Fatalf("unarrived=%d, want %d", got, pushSendsKept)
	}
	for i := range 5 {
		id := fmt.Sprintf("withdrawn%03d", i)
		at := base.Add(time.Minute + time.Duration(i)*time.Second)
		if err := r.RecordPushSent(device.ID, id, "turn.completed", at); err != nil {
			t.Fatal(err)
		}
		if err := r.SettlePushSent(id, false, at); err != nil {
			t.Fatal(err)
		}
	}
	if got := unarrived(); got != pushSendsKept {
		t.Fatalf("sends through a relay without receipts evicted confirmed history: unarrived=%d, want %d", got, pushSendsKept)
	}

	if err := r.RecordPushSent(device.ID, "stranded", "session.waiting", base); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordPushSent(device.ID, "newest", "session.waiting", base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := r.SettlePushSent("newest", true, base.Add(48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	var stranded, confirmed int
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM push_sends WHERE push_id='stranded'`).Scan(&stranded); err != nil {
		t.Fatal(err)
	}
	if err := r.read.QueryRow(`SELECT COUNT(*) FROM push_sends WHERE confirmed=1`).Scan(&confirmed); err != nil {
		t.Fatal(err)
	}
	if stranded != 0 || confirmed != pushSendsKept {
		t.Fatalf("a confirmed send should keep the newest %d confirmed and drop a day-old unconfirmed one: stranded=%d confirmed=%d", pushSendsKept, stranded, confirmed)
	}
}

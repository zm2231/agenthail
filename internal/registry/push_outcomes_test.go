package registry

import (
	"errors"
	"testing"
	"time"
)

func TestPushOutcomesKeepTheLatestOfEachWhateverOrderTheyLand(t *testing.T) {
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
	base := time.Date(2026, 11, 2, 23, 0, 0, 0, time.UTC)
	writes := []struct {
		event string
		at    time.Time
		err   error
	}{
		{"turn.completed", base.Add(4 * time.Second), nil},
		{"session.waiting", base.Add(3 * time.Second), errors.New("newest failure")},
		{"session.waiting", base.Add(2 * time.Second), nil},
		{"turn.completed", base.Add(time.Second), errors.New("older failure")},
	}
	for _, write := range writes {
		if err := r.RecordPushOutcome(device.ID, write.event, write.at, write.err); err != nil {
			t.Fatal(err)
		}
	}
	statuses, err := r.PushStatuses(base)
	if err != nil || len(statuses) != 1 {
		t.Fatalf("statuses=%+v err=%v", statuses, err)
	}
	got := statuses[0]
	if got.EventType != "turn.completed" || got.AttemptedAt != formatTimestamp(base.Add(4*time.Second)) || got.SucceededAt != formatTimestamp(base.Add(4*time.Second)) || got.Error != "newest failure" || got.FailedAt != formatTimestamp(base.Add(3*time.Second)) {
		t.Fatalf("an older outcome overwrote a newer one: %+v", got)
	}
}

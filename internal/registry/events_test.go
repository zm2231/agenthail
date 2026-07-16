package registry

import (
	"bytes"
	"testing"
	"time"
)

func TestDaemonEventsPersistInOrderAndPruneAtomically(t *testing.T) {
	r := openTestRegistry(t)
	for index := 0; index < 4; index++ {
		if _, err := r.AppendDaemonEvent("session.updated", "session", []byte{byte(index)}, time.Now(), 3); err != nil {
			t.Fatal(err)
		}
	}
	events, err := r.RecentDaemonEvents(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].ID >= events[1].ID || events[1].ID >= events[2].ID {
		t.Fatalf("events=%+v", events)
	}
	if !bytes.Equal(events[0].Payload, []byte{1}) || !bytes.Equal(events[2].Payload, []byte{3}) {
		t.Fatalf("payloads=%v %v", events[0].Payload, events[2].Payload)
	}
}

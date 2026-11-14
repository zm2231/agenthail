package registry

import (
	"testing"
	"time"
)

func TestDeviceStreamsAreKeptForPairedDevicesNewestFirstAndBoundedPerDevice(t *testing.T) {
	r := openTestRegistry(t)
	phone, tablet := pairNamedDevice(t, r, "iPhone"), pairNamedDevice(t, r, "iPad")
	start := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	for i := range deviceStreamsKept + 3 {
		opened := start.Add(time.Duration(i) * time.Minute)
		stream := DeviceStream{Route: "events", OpenedAt: opened, ClosedAt: opened.Add(30 * time.Second), Ended: StreamEndedClientClosed, Bytes: int64(i), TailnetPath: "direct"}
		if i%2 == 0 {
			stream.LastFlushAt = opened.Add(20 * time.Second)
		}
		if err := r.RecordDeviceStream(phone, stream); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.RecordDeviceStream(tablet, DeviceStream{Route: "events", OpenedAt: start, ClosedAt: start.Add(time.Second), Ended: StreamEndedServer, TailnetPath: "unknown"}); err != nil {
		t.Fatal(err)
	}
	all, err := r.DeviceStreams(phone, "events", start, start.Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != deviceStreamsKept || all[0].Bytes != deviceStreamsKept+2 || all[len(all)-1].Bytes != 3 {
		t.Fatalf("kept %d streams, newest %+v, oldest %+v", len(all), all[0], all[len(all)-1])
	}
	if !all[0].LastFlushAt.Equal(all[0].OpenedAt.Add(20*time.Second)) || !all[1].LastFlushAt.IsZero() {
		t.Fatalf("last flush times were not kept as recorded: %+v %+v", all[0], all[1])
	}
	if tablets, err := r.DeviceStreams(tablet, "events", start, start.Add(time.Hour)); err != nil || len(tablets) != 1 {
		t.Fatalf("another device's streams were pruned: %+v %v", tablets, err)
	}

	at := start.Add(100*time.Minute + 15*time.Second)
	window, err := r.DeviceStreams(phone, "events", at, at)
	if err != nil || len(window) != 1 || window[0].Bytes != 100 {
		t.Fatalf("streams open at %s: %+v %v", at, window, err)
	}
	if other, err := r.DeviceStreams(phone, "catalog-events", start, start.Add(24*time.Hour)); err != nil || len(other) != 0 {
		t.Fatalf("another route's streams: %+v %v", other, err)
	}

	if err := r.RevokeDevice(tablet); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDeviceStream(tablet, DeviceStream{Route: "events", OpenedAt: start.Add(time.Hour), ClosedAt: start.Add(time.Hour + time.Second), Ended: StreamEndedUnauthorized}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordDeviceStream("no-such-device", DeviceStream{Route: "events", OpenedAt: start, ClosedAt: start, Ended: StreamEndedServer}); err != nil {
		t.Fatal(err)
	}
	if tablets, err := r.DeviceStreams(tablet, "events", start, start.Add(2*time.Hour)); err != nil || len(tablets) > 1 {
		t.Fatalf("a revoked device's stream was recorded: %+v %v", tablets, err)
	}
}

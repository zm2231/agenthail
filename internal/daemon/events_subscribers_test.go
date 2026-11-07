package daemon

import (
	"strconv"
	"strings"
	"testing"
	"time"
)

func openReadingStream(t *testing.T, d *Daemon) (*stalledClient, <-chan struct{}) {
	t.Helper()
	client, ended := openStalledStream(t, d)
	close(client.release)
	return client, ended
}

func TestAStalledEventStreamDoesNotHoldBackAnotherClient(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	stalled, _ := openStalledStream(t, d)
	healthy, healthyEnded := openReadingStream(t, d)
	first := d.events.cursor() + 1
	published := make(chan apiEvent, 1)
	go func() {
		var last apiEvent
		for index := range 200 {
			event, err := d.events.publish("test.burst", strconv.Itoa(index), nil)
			if err != nil {
				t.Error(err)
			}
			last = event
		}
		published <- last
	}()
	var last apiEvent
	select {
	case last = <-published:
	case <-time.After(10 * time.Second):
		t.Fatal("publishing waited on the stalled stream")
	}
	body := awaitReceived(t, healthy, healthyEnded, "id: "+strconv.FormatUint(last.ID, 10)+"\n")
	for id := first; id <= last.ID; id++ {
		if !strings.Contains(body, "id: "+strconv.FormatUint(id, 10)+"\n") {
			t.Fatalf("the reading client is missing event %d", id)
		}
	}
	if got := stalled.received(); got != "" {
		t.Fatalf("the stalled client took %d bytes; it should still be waiting on its first write", len(got))
	}
}

func TestEventStreamsReadOneHistoryAndPublishNothingOfTheirOwn(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	type reader struct {
		client *stalledClient
		ended  <-chan struct{}
	}
	readers := make([]reader, 3)
	for index := range readers {
		client, ended := openReadingStream(t, d)
		readers[index] = reader{client, ended}
	}
	before := d.events.cursor()
	last := publishBurst(t, d, 5)
	if last.ID != before+5 {
		t.Fatalf("five publishes moved the history from %d to %d with three streams open", before, last.ID)
	}
	for index, r := range readers {
		body := awaitReceived(t, r.client, r.ended, "id: "+strconv.FormatUint(last.ID, 10)+"\n")
		for id := before + 1; id <= last.ID; id++ {
			if count := strings.Count(body, "id: "+strconv.FormatUint(id, 10)+"\n"); count != 1 {
				t.Fatalf("stream %d sent event %d %d times", index, id, count)
			}
		}
	}
	if after := d.events.cursor(); after != last.ID {
		t.Fatalf("the streams added events of their own: the history moved to %d after the last publish %d", after, last.ID)
	}
}

package daemon

import (
	"errors"
	"reflect"
	"sort"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestQueuedBehindMatchesTheQueueListing(t *testing.T) {
	r, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	for _, id := range []string{"target", "busy", "done", "writer", "runner", "relayed", "old"} {
		if err := r.RecordObservedSession(surface.Session{ID: id, Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{surface.PersonSessionID, surface.SystemSessionID} {
		if err := r.EnsureSender(id); err != nil {
			t.Fatal(err)
		}
	}
	queue := func(target, key, source string, author surface.Author) int64 {
		t.Helper()
		id, err := r.QueueMessageWithOptions(target, "hello", key, surface.SendOptions{SourceSessionID: source, Author: author})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	claim := func(target string) int64 {
		t.Helper()
		item, err := r.ClaimNextMessage(target, time.Now())
		if err != nil || item == nil {
			t.Fatalf("claim %s: item=%v err=%v", target, item, err)
		}
		return item.ID
	}
	queue("target", "writer-1", "writer", "")
	queue("target", "writer-2", "writer", "")
	queue("busy", "writer-3", "writer", "")
	claim("busy")
	queue("busy", "runner", "runner", surface.AuthorAgenthail)
	queue("target", "relayed", "relayed", surface.AuthorPerson)
	queue("target", "person", surface.PersonSessionID, "")
	queue("target", "system", surface.SystemSessionID, "")
	queue("target", "self", "target", "")
	queue("target", "anonymous", "", "")
	queue("done", "delivered", "old", "")
	if err := r.AckMessage(claim("done")); err != nil {
		t.Fatal(err)
	}
	queue("done", "dead", "old", "")
	if err := r.DeadLetterMessage(claim("done"), errors.New("refused")); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CancelMessage(queue("done", "canceled", "old", "")); err != nil {
		t.Fatal(err)
	}
	d := newTestDaemon(t, r, nil)
	listed, err := r.ListQueue(false, d.adapters())
	if err != nil {
		t.Fatal(err)
	}
	sources := map[string]map[string]bool{}
	for _, row := range listed {
		source := row.SourceSessionID
		if row.Status == "dead" || source == "" || source == row.SessionID || source == surface.SystemSessionID || source == surface.PersonSessionID || row.Author == surface.AuthorPerson {
			continue
		}
		target := d.publicSessionID(row.SessionID)
		if sources[target] == nil {
			sources[target] = map[string]bool{}
		}
		sources[target][d.publicSessionID(source)] = true
	}
	want := map[string][]string{}
	for target, from := range sources {
		for id := range from {
			want[target] = append(want[target], id)
		}
		sort.Strings(want[target])
	}
	got, err := d.queuedBehind()
	if err != nil {
		t.Fatal(err)
	}
	if len(want) == 0 || !reflect.DeepEqual(got, want) {
		t.Fatalf("queuedBehind=%v, the queue listing gives %v", got, want)
	}
}

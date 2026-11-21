package registry

import (
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestQueuedSendersAreTheAgentsWithOpenRowsForAnotherSession(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "target", "writer", "runner", "relayed", "old")
	for _, id := range []string{surface.PersonSessionID, surface.SystemSessionID} {
		if err := r.EnsureSender(id); err != nil {
			t.Fatal(err)
		}
	}
	queue := func(key, source string, author surface.Author, status string) {
		t.Helper()
		id, err := r.QueueMessageWithOptions("target", "hello", key, surface.SendOptions{SourceSessionID: source, Author: author})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := r.db.Exec(`UPDATE message_queue SET status=? WHERE id=?`, status, id); err != nil {
			t.Fatal(err)
		}
	}
	queue("writer-1", "writer", "", "pending")
	queue("writer-2", "writer", "", "pending")
	queue("runner", "runner", surface.AuthorAgenthail, "inflight")
	queue("relayed", "relayed", surface.AuthorPerson, "pending")
	queue("person", surface.PersonSessionID, "", "pending")
	queue("system", surface.SystemSessionID, "", "pending")
	queue("self", "target", "", "pending")
	queue("anonymous", "", "", "pending")
	for _, status := range []string{"delivered", "canceled", "dead"} {
		queue("old-"+status, "old", "", status)
	}
	senders, err := r.QueuedSenders()
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(senders, func(i, j int) bool { return senders[i].SourceSessionID < senders[j].SourceSessionID })
	want := []QueuedSender{{SessionID: "target", SourceSessionID: "runner"}, {SessionID: "target", SourceSessionID: "writer"}}
	if !reflect.DeepEqual(senders, want) {
		t.Fatalf("senders=%+v, want %+v", senders, want)
	}
	for _, step := range queryPlan(t, r, queuedSendersQuery, surface.SystemSessionID, surface.PersonSessionID, string(surface.AuthorPerson)) {
		if strings.Contains(step, "message_queue") && !strings.Contains(step, "INDEX message_queue_open") {
			t.Errorf("queued senders read message_queue without the open-row index: %q", step)
		}
	}
}

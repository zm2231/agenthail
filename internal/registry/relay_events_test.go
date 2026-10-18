package registry

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestRelayProvenanceBindsQueueRecipientAndInputAcrossRestart(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "source", "target", "other", "sink")
	if _, err := r.AddRoute("source", "target", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "one", Events: []surface.Completion{{ID: "one", Text: "reply"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	events, _ := r.PendingRelayEvents(routes[0])
	queueID, err := r.ConsumeRelay(routes[0], events[0], "[agenthail relay hops=1 id=1 turn=one source=@source] reply", "steer", "")
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.QueueItem(queueID, nil)
	if err != nil || item.Message != "[agenthail relay hops=1 id=1 turn=one source=@source] reply" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	path := r.Path()
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.AddRoute("target", "sink", ".*"); err != nil {
		t.Fatal(err)
	}
	batch := surface.CompletionBatch{Cursor: "received", Events: []surface.Completion{{ID: "answer", InputID: "input", Text: "answer", User: "<cross-session-message from=\"uds:sender\">\n" + item.Message + "\n</cross-session-message>"}}}
	if _, err := r.db.Exec(`CREATE TRIGGER fail_capture BEFORE INSERT ON relay_events BEGIN SELECT RAISE(ABORT,'capture failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("target", "", batch); err == nil {
		t.Fatal("capture failure was swallowed")
	}
	var bound int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM relay_provenance WHERE queue_id=?`, queueID).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("provenance binding survived failed capture: %d %v", bound, err)
	}
	if _, err := r.db.Exec(`DROP TRIGGER fail_capture`); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("target", "", batch); err != nil {
		t.Fatal(err)
	}
	batch = surface.CompletionBatch{Cursor: "more", Events: []surface.Completion{
		{ID: "copied", InputID: "another-input", User: item.Message, Text: "copied"},
		{ID: "same-input", InputID: "input", User: item.Message, Text: "same input"},
		{ID: "unknown", User: "[agenthail relay hops=99 id=1 turn=missing source=@source] unknown", Text: "unknown"},
	}}
	if err := r.CaptureRelayCompletions("target", "received", batch); err != nil {
		t.Fatal(err)
	}
	routes, _ = r.ListRoutes()
	events, err = r.PendingRelayEvents(routes[1])
	if err != nil || len(events) != 4 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	for i, want := range []int{1, 0, 1, 0} {
		if events[i].RelayHops != want {
			t.Fatalf("event=%+v want hops=%d", events[i], want)
		}
	}
	if err := r.CaptureRelayCompletions("other", "", surface.CompletionBatch{Cursor: "other", Events: []surface.Completion{{ID: "answer", InputID: "input", User: item.Message}}}); err != nil {
		t.Fatal(err)
	}
	events, err = r.PendingRelayEvents(RouteRow{FromSession: "other"})
	if err != nil || len(events) != 1 || events[0].RelayHops != 0 {
		t.Fatalf("provenance crossed recipient boundary: %+v %v", events, err)
	}
}

func TestRelayConsumptionRollsBackEveryDurableEffect(t *testing.T) {
	for _, table := range []string{"message_queue", "delivery_intents", "relay_deliveries", "delivery_history"} {
		t.Run(table, func(t *testing.T) {
			r := openTestRegistry(t)
			register(t, r, "source", "target")
			if _, err := r.AddRouteWithOptions("source", "target", ".*", true); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "after-one", Events: []surface.Completion{{ID: "one", Text: "result"}}}); err != nil {
				t.Fatal(err)
			}
			routes, _ := r.ListRoutes()
			events, err := r.PendingRelayEvents(routes[0])
			if err != nil || len(events) != 1 {
				t.Fatalf("events=%v err=%v", events, err)
			}
			if _, err := r.db.Exec(fmt.Sprintf(`CREATE TRIGGER fail_insert BEFORE INSERT ON %s BEGIN SELECT RAISE(ABORT,'injected failure'); END`, table)); err != nil {
				t.Fatal(err)
			}
			if _, err := r.ConsumeRelay(routes[0], events[0], "payload", "queue", ""); err == nil {
				t.Fatal("injected failure was swallowed")
			}
			after, _ := r.ListRoutes()
			if !after[0].Active || after[0].EventCursor != 0 || after[0].FireCount != 0 || r.QueueCount("target") != 0 {
				t.Fatalf("partial transaction: %+v", after[0])
			}
			var intents int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM delivery_intents`).Scan(&intents); err != nil || intents != 0 {
				t.Fatalf("intents=%d err=%v", intents, err)
			}
			var tokens int
			if err := r.db.QueryRow(`SELECT COUNT(*) FROM relay_provenance`).Scan(&tokens); err != nil || tokens != 0 {
				t.Fatalf("tokens=%d err=%v", tokens, err)
			}
			if _, err := r.db.Exec(`DROP TRIGGER fail_insert`); err != nil {
				t.Fatal(err)
			}
			id, err := r.ConsumeRelay(after[0], events[0], "payload", "queue", "")
			if err != nil || id == 0 {
				t.Fatalf("id=%d err=%v", id, err)
			}
			after, _ = r.ListRoutes()
			if after[0].Active || after[0].EventCursor != events[0].ID || after[0].FireCount != 1 || r.QueueCount("target") != 1 {
				t.Fatalf("retry=%+v", after[0])
			}
			if _, err := r.ConsumeRelay(routes[0], events[0], "payload", "queue", ""); err == nil || r.QueueCount("target") != 1 {
				t.Fatalf("stale consumer duplicated work: %v", err)
			}
		})
	}
}

func TestRelayCaptureRollsBackCursorAndReplaysAfterReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "source", "target")
	if _, err := r.AddRoute("source", "target", ".*"); err != nil {
		t.Fatal(err)
	}
	batch := surface.CompletionBatch{Cursor: "after-two", Events: []surface.Completion{{ID: "one", Text: "first"}, {ID: "two", Text: "second"}}}
	if _, err := r.db.Exec(`CREATE TRIGGER fail_second BEFORE INSERT ON relay_events WHEN NEW.completion_id='two' BEGIN SELECT RAISE(ABORT,'disk full'); END`); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", batch); err == nil {
		t.Fatal("capture should fail")
	}
	if cursor, err := r.RelaySourceCursor("source"); err != nil || cursor != "" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
	if _, err := r.db.Exec(`DROP TRIGGER fail_second`); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", batch); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if err := r.CaptureRelayCompletions("source", batch.Cursor, batch); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	events, err := r.PendingRelayEvents(routes[0])
	if err != nil || len(events) != 2 || events[0].CompletionID != "one" || events[1].CompletionID != "two" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if _, err := r.ConsumeRelay(routes[0], events[0], "first", "queue", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute("source", "target", "second"); err != nil {
		t.Fatal(err)
	}
	routes, _ = r.ListRoutes()
	if routes[0].PendingCount != 1 || routes[1].PendingCount != 0 {
		t.Fatalf("route starting positions=%+v", routes)
	}
}

func TestConcurrentRelayConsumersQueueOnce(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "source", "target")
	if _, err := r.AddRoute("source", "target", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "one", Events: []surface.Completion{{ID: "one", Text: "result"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	events, _ := r.PendingRelayEvents(routes[0])
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() { _, _ = r.ConsumeRelay(routes[0], events[0], "result", "queue", "") })
	}
	wg.Wait()
	routes, _ = r.ListRoutes()
	if r.QueueCount("target") != 1 || routes[0].FireCount != 1 {
		t.Fatalf("route=%+v queue=%d", routes[0], r.QueueCount("target"))
	}
}

func TestRelaySchemaUpgradePreservesRoutesAndIntents(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "source", "target")
	if _, err := r.AddRoute("source", "target", ".*"); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"handoff", "event_cursor", "last_goal_key", "last_error"} {
		if _, err := r.db.Exec(`ALTER TABLE routes DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.db.Exec(`DROP TABLE relay_sources; DROP TABLE relay_events; DROP TABLE relay_provenance; PRAGMA user_version=12`); err != nil {
		t.Fatal(err)
	}
	if err := r.migrate(); err != nil {
		t.Fatal(err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].Handoff != HandoffTurnFinished {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "baseline"}); err != nil {
		t.Fatal(err)
	}
}

func TestRemovingLastRelayClearsCheckpointAndCapturedEvents(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "source", "target")
	first, err := r.AddRoute("source", "target", "one")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.AddRoute("source", "target", "two")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "checkpoint", Events: []surface.Completion{{ID: "one", Text: "retained"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveRoute(first); err != nil {
		t.Fatal(err)
	}
	if cursor, err := r.RelaySourceCursor("source"); err != nil || cursor != "checkpoint" {
		t.Fatalf("shared cursor=%q err=%v", cursor, err)
	}
	if err := r.RemoveRoute(second); err != nil {
		t.Fatal(err)
	}
	if cursor, err := r.RelaySourceCursor("source"); err != nil || cursor != "" {
		t.Fatalf("reset cursor=%q err=%v", cursor, err)
	}
	var count int
	if err := r.db.QueryRow(`SELECT COUNT(*) FROM relay_events`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("events=%d err=%v", count, err)
	}
}

func TestRelayMigrationMarksExistingQueuedRelayIntents(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "source", "target")
	queueID, err := r.QueueRelayMessageWithOptions("target", "pending", "relay:1:turn", 1, surface.SendOptions{SourceSessionID: "source"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`UPDATE delivery_intents SET relayed=0; PRAGMA user_version=12`); err != nil {
		t.Fatal(err)
	}
	if err := r.migrate(); err != nil {
		t.Fatal(err)
	}
	id, err := queuedDeliveryIntentID(r.db, queueID)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := r.DeliveryIntent(id)
	if err != nil || !intent.Relayed {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
}

func TestQuotedRelayEnvelopeKeepsTheQueuedHopCount(t *testing.T) {
	payloads := map[string]string{
		"quoted body":   "[agenthail relay hops=8 id=1 turn=completion-42 source=worker one] Quoted input: [agenthail relay hops=1 id=1 turn=completion-41 source=previous] prompt",
		"crafted alias": "[agenthail relay hops=8 id=1 turn=completion-42 source=@worker turn=missing] turn=real] answer",
	}
	for name, wrap := range map[string]func(string) string{
		"plain": func(message string) string { return message },
		"peer": func(message string) string {
			return "<cross-session-message from=\"uds:sender\">\n" + message + "\n</cross-session-message>"
		},
	} {
		for label, payload := range payloads {
			t.Run(name+"/"+label, func(t *testing.T) {
				r := openTestRegistry(t)
				register(t, r, "source", "target", "sink")
				if _, err := r.AddRoute("source", "target", ".*"); err != nil {
					t.Fatal(err)
				}
				if err := r.CaptureRelayCompletions("source", "", surface.CompletionBatch{Cursor: "one", Events: []surface.Completion{{ID: "completion-42", Text: "reply"}}}); err != nil {
					t.Fatal(err)
				}
				routes, _ := r.ListRoutes()
				events, _ := r.PendingRelayEvents(routes[0])
				events[0].RelayHops = 7
				queueID, err := r.ConsumeRelay(routes[0], events[0], payload, "queue", "")
				if err != nil {
					t.Fatal(err)
				}
				var hops int
				if err := r.db.QueryRow(`SELECT relay_hops FROM message_queue WHERE id=?`, queueID).Scan(&hops); err != nil || hops != 8 {
					t.Fatalf("hops=%d err=%v", hops, err)
				}
				if _, err := r.AddRoute("target", "sink", ".*"); err != nil {
					t.Fatal(err)
				}
				if err := r.CaptureRelayCompletions("target", "", surface.CompletionBatch{Cursor: "answered", Events: []surface.Completion{{ID: "answer", InputID: "input", Text: "answer", User: wrap(payload)}}}); err != nil {
					t.Fatal(err)
				}
				routes, _ = r.ListRoutes()
				events, err = r.PendingRelayEvents(routes[1])
				if err != nil || len(events) != 1 || events[0].RelayHops != 8 {
					t.Fatalf("events=%+v err=%v", events, err)
				}
			})
		}
	}
}

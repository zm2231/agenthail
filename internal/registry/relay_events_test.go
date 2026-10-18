package registry

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
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
	item, err := r.QueueItem(queueID)
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

func overlappingClaudeRows(t *testing.T) (*Registry, string, surface.Session) {
	t.Helper()
	stubLiveProcesses(t, 101, 202)
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	survivor := surface.Session{ID: "survivor", Surface: surface.KindClaude, PID: 202, Transcript: "/tmp/shared.jsonl"}
	for _, s := range []surface.Session{{ID: "old", Surface: surface.KindClaude, PID: 101, Transcript: "/tmp/shared.jsonl"}, survivor} {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	register(t, r, "target", "observer", "sink")
	return r, path, survivor
}

func mergeAndReopen(t *testing.T, r *Registry, path string, survivor surface.Session) *Registry {
	t.Helper()
	stubLiveProcesses(t, 202)
	if err := r.RegisterSession(survivor); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func pendingCompletions(t *testing.T, r *Registry) map[int64]string {
	t.Helper()
	routes, err := r.ListRoutes()
	if err != nil {
		t.Fatal(err)
	}
	pending := map[int64]string{}
	for _, route := range routes {
		events, err := r.PendingRelayEvents(route)
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		for _, event := range events {
			ids = append(ids, event.CompletionID)
		}
		pending[route.ID] = route.FromSession + ":" + strings.Join(ids, ",")
	}
	return pending
}

func TestClaudeRowsOfOneTranscriptShareOneRelayLog(t *testing.T) {
	r, path, survivor := overlappingClaudeRows(t)
	own, err := r.AddRoute("survivor", "observer", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("survivor", "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "A", Text: "A"}}}); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.AddRouteWithOptions("old", "target", ".*", true)
	if err != nil {
		t.Fatal(err)
	}
	if cursor, err := r.RelaySourceCursor("old"); err != nil || cursor != "c1" {
		t.Fatalf("old row cursor=%q err=%v", cursor, err)
	}
	if err := r.CaptureRelayCompletions("old", "", surface.CompletionBatch{Cursor: "stale", Events: []surface.Completion{{ID: "A", Text: "A"}, {ID: "S", Text: "S"}}}); !errors.Is(err, ErrRelayCursorMoved) {
		t.Fatalf("stale capture err=%v", err)
	}
	if err := r.CaptureRelayCompletions("old", "c1", surface.CompletionBatch{Cursor: "c2", Events: []surface.Completion{{ID: "X", Text: "X"}}}); err != nil {
		t.Fatal(err)
	}
	want := map[int64]string{own: "survivor:A,X", fresh: "old:X"}
	if got := pendingCompletions(t, r); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pending=%v want %v", got, want)
	}
	r = mergeAndReopen(t, r, path, survivor)
	if _, err := r.Session("old"); err == nil {
		t.Fatal("exited row was not merged")
	}
	want = map[int64]string{own: "survivor:A,X", fresh: "survivor:X"}
	if got := pendingCompletions(t, r); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pending=%v want %v", got, want)
	}
	routes, _ := r.ListRoutes()
	events, _ := r.PendingRelayEvents(routes[1])
	if _, err := r.ConsumeRelay(routes[1], events[0], "X", "queue", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("survivor", "c2", surface.CompletionBatch{Cursor: "c3", Events: []surface.Completion{{ID: "Y", Text: "Y"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ = r.ListRoutes()
	if routes[0].PendingCount != 3 || routes[1].Active || routes[1].FireCount != 1 || r.QueueCount("target") != 1 {
		t.Fatalf("routes=%+v queued=%d", routes, r.QueueCount("target"))
	}
}

func TestClaudeMergeDeliversQueuedWorkForTheExitedRowThroughTheSurvivor(t *testing.T) {
	r, path, survivor := overlappingClaudeRows(t)
	if _, err := r.AddRoute("target", "old", ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueRelayMessageWithOptions("old", "relayed", "relay:9:turn", 1, surface.SendOptions{SourceSessionID: "target"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions("old", "direct", "", surface.SendOptions{SourceSessionID: "sink"}); err != nil {
		t.Fatal(err)
	}
	r = mergeAndReopen(t, r, path, survivor)
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].ToSession != "survivor" || !routes[0].Active {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if got := r.QueueCount("survivor"); got != 2 {
		t.Fatalf("survivor queue=%d", got)
	}
}

func TestRelayHopsSurviveCaptureThroughEitherRowOfAConversation(t *testing.T) {
	for _, via := range []string{"old", "survivor"} {
		t.Run(via, func(t *testing.T) {
			r, _, _ := overlappingClaudeRows(t)
			t.Cleanup(func() { r.Close() })
			route, err := r.AddRoute("survivor", "sink", ".*")
			if err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureRelayCompletions(via, "", surface.CompletionBatch{Cursor: "c1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := r.QueueRelayMessageWithOptions("old", "[agenthail relay hops=8 id=4 turn=up source=upstream] go", "relay:4:up", 8, surface.SendOptions{SourceSessionID: "target"}); err != nil {
				t.Fatal(err)
			}
			if err := r.CaptureRelayCompletions(via, "c1", surface.CompletionBatch{Cursor: "c2", Events: []surface.Completion{{ID: "out", InputID: "in", User: "[agenthail relay hops=8 id=4 turn=up source=upstream] go", Text: "reply"}}}); err != nil {
				t.Fatal(err)
			}
			routes, _ := r.ListRoutes()
			events, err := r.PendingRelayEvents(routes[0])
			if err != nil || routes[0].ID != route || len(events) != 1 || events[0].RelayHops != 8 {
				t.Fatalf("events=%+v err=%v", events, err)
			}
		})
	}
}

func TestRemovingTheExitedRowsLastRelayKeepsTheConversationLog(t *testing.T) {
	r, path, survivor := overlappingClaudeRows(t)
	own, err := r.AddRoute("survivor", "observer", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("survivor", "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "A", Text: "A"}, {ID: "B", Text: "B"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	events, _ := r.PendingRelayEvents(routes[0])
	for _, event := range events {
		if _, err := r.ConsumeRelay(routes[0], event, event.Message, "queue", ""); err != nil {
			t.Fatal(err)
		}
		routes[0].EventCursor = event.ID
	}
	other, err := r.AddRoute("old", "target", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("old", "c1", surface.CompletionBatch{Cursor: "c2", Events: []surface.Completion{{ID: "C", Text: "C"}}}); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveRoute(other); err != nil {
		t.Fatal(err)
	}
	r = mergeAndReopen(t, r, path, survivor)
	if cursor, err := r.RelaySourceCursor("survivor"); err != nil || cursor != "c2" {
		t.Fatalf("cursor=%q err=%v", cursor, err)
	}
	want := map[int64]string{own: "survivor:C"}
	if got := pendingCompletions(t, r); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("pending=%v want %v", got, want)
	}
}

func TestClaudeLaunchRecordRelayStartsAtTheConversationLog(t *testing.T) {
	stubLiveProcesses(t, 202)
	r := openTestRegistry(t)
	transcript := filepath.Join(t.TempDir(), "conv.jsonl")
	live := surface.Session{ID: "live", Surface: surface.KindClaude, PID: 202, Transcript: transcript}
	for _, s := range []surface.Session{live, {ID: "conv", Surface: surface.KindClaude}} {
		if err := r.RegisterSession(s); err != nil {
			t.Fatal(err)
		}
	}
	register(t, r, "target", "observer")
	if _, err := r.RelaySourceCursor("conv"); !errors.Is(err, errNoRelayTranscript) {
		t.Fatalf("launch record cursor err=%v", err)
	}
	if _, err := r.AddRoute("live", "observer", ".*"); err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("live", "", surface.CompletionBatch{Cursor: "c1", Events: []surface.Completion{{ID: "A", Text: "A"}}}); err != nil {
		t.Fatal(err)
	}
	route, err := r.AddRoute("conv", "target", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.RegisterSession(surface.Session{ID: "bridge", Surface: surface.KindClaude, PID: 303, Transcript: transcript}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Session("conv"); err == nil {
		t.Fatal("launch record was not merged")
	}
	if err := r.CaptureRelayCompletions("bridge", "c1", surface.CompletionBatch{Cursor: "c2", Events: []surface.Completion{{ID: "B", Text: "B"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	for _, row := range routes {
		if row.ID != route {
			continue
		}
		events, err := r.PendingRelayEvents(row)
		if err != nil || len(events) != 1 || events[0].CompletionID != "B" {
			t.Fatalf("events=%+v err=%v", events, err)
		}
		return
	}
	t.Fatalf("route %d missing: %+v", route, routes)
}

func TestClaudeMergeKeepsAFreshOneShotBesideASpentOne(t *testing.T) {
	r, path, survivor := overlappingClaudeRows(t)
	spent, err := r.AddRouteWithOptions("old", "observer", ".*", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.CaptureRelayCompletions("old", "", surface.CompletionBatch{Cursor: "old-1", Events: []surface.Completion{{ID: "first", Text: "first"}}}); err != nil {
		t.Fatal(err)
	}
	routes, _ := r.ListRoutes()
	events, _ := r.PendingRelayEvents(routes[0])
	if _, err := r.ConsumeRelay(routes[0], events[0], "first", "queue", ""); err != nil {
		t.Fatal(err)
	}
	fresh, err := r.AddRouteWithOptions("survivor", "observer", ".*", true)
	if err != nil {
		t.Fatal(err)
	}
	r = mergeAndReopen(t, r, path, survivor)
	routes, err = r.ListRoutes()
	if err != nil || len(routes) != 2 || routes[0].ID != spent || routes[0].Active || routes[1].ID != fresh || !routes[1].Active {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if err := r.CaptureRelayCompletions("survivor", "old-1", surface.CompletionBatch{Cursor: "survivor-2", Events: []surface.Completion{{ID: "next", Text: "next"}}}); err != nil {
		t.Fatal(err)
	}
	events, err = r.PendingRelayEvents(routes[1])
	if err != nil || len(events) != 1 || events[0].CompletionID != "next" {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	if _, err := r.ConsumeRelay(routes[1], events[0], "next", "queue", ""); err != nil {
		t.Fatal(err)
	}
	routes, _ = r.ListRoutes()
	if routes[1].Active || routes[1].FireCount != 1 {
		t.Fatalf("fresh one-shot=%+v", routes[1])
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

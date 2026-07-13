package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
	_ "modernc.org/sqlite"
)

func openTestRegistry(t *testing.T) *Registry {
	t.Helper()
	r, err := Open(filepath.Join(t.TempDir(), "registry.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func register(t *testing.T, r *Registry, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestOpenMigratesLegacyQueue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE sessions(id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '', has_local INTEGER NOT NULL DEFAULT 0, registered_at TEXT NOT NULL DEFAULT '', updated_at TEXT NOT NULL DEFAULT '');
		CREATE TABLE message_queue(id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, message TEXT NOT NULL, queued_at TEXT NOT NULL DEFAULT '', delivered INTEGER NOT NULL DEFAULT 0);
		INSERT INTO sessions(id,surface) VALUES('s','codex');
		INSERT INTO message_queue(session_id,message,delivered) VALUES('s','old',1);`)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var status string
	if err := r.db.QueryRow(`SELECT status FROM message_queue WHERE message='old'`).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "delivered" {
		t.Fatalf("status=%q", status)
	}
	var queueHops int
	if err := r.db.QueryRow(`SELECT relay_hops FROM message_queue WHERE message='old'`).Scan(&queueHops); err != nil || queueHops != 0 {
		t.Fatalf("queue relay_hops=%d err=%v", queueHops, err)
	}
	if err := r.RegisterSession(surface.Session{ID: "runtime", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveRuntimeState("runtime", surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState("runtime")
	if err != nil || !found || state.RelayHops != 0 {
		t.Fatalf("runtime=%+v found=%v err=%v", state, found, err)
	}
}

func TestForeignKeysAndChannelValidation(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.SetAlias("worker", "s"); err != nil {
		t.Fatal(err)
	}
	if err := r.AddToChannel("missing", "s"); err == nil {
		t.Fatal("missing channel accepted")
	}
	if _, err := r.db.Exec(`DELETE FROM sessions WHERE id='s'`); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LookupAlias("worker"); err != sql.ErrNoRows {
		t.Fatalf("alias survived cascade: %v", err)
	}
}

func TestReplaceAliasKeepsOneNamePerSession(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "session-alias", Surface: surface.KindClaude, Name: "Alias test"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("first", session.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.ReplaceAlias("second", session.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.LookupAlias("first"); err == nil {
		t.Fatal("old alias still resolves")
	}
	alias, err := r.ReverseAlias(session.ID)
	if err != nil {
		t.Fatal(err)
	}
	if alias != "second" {
		t.Fatalf("alias = %q, want second", alias)
	}
}

func TestSessionReturnsCompleteRegisteredSnapshot(t *testing.T) {
	r := openTestRegistry(t)
	want := surface.Session{ID: "full", Surface: surface.KindClaude, Name: "writer", Cwd: "/tmp/project", PID: 42, Status: surface.StatusBusy, Transcript: "/tmp/thread.jsonl", HasLocal: true, Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Surface != want.Surface || got.Name != want.Name || got.Cwd != want.Cwd || got.PID != want.PID || got.Status != want.Status || got.Transcript != want.Transcript || got.HasLocal != want.HasLocal || got.Source != want.Source || got.Transport != want.Transport {
		t.Fatalf("session=%+v want=%+v", got, want)
	}
}

func TestRegisterSessionPreservesManagedCodexOwnershipAcrossDiscovery(t *testing.T) {
	r := openTestRegistry(t)
	managed := surface.Session{ID: "managed", Surface: surface.KindCodex, Name: "Started here", Status: surface.StatusBusy, Source: "agenthail", Transport: "managed"}
	if err := r.RegisterSession(managed); err != nil {
		t.Fatal(err)
	}
	discovered := managed
	discovered.Name = "Updated title"
	discovered.Status = surface.SessionStatus("notLoaded")
	discovered.Source = "vscode"
	discovered.Transport = "readOnly"
	if err := r.RegisterSession(discovered); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "agenthail" || got.Transport != "managed" || got.Name != "Updated title" || got.Status != surface.SessionStatus("notLoaded") {
		t.Fatalf("session=%+v", got)
	}
	desktop := surface.Session{ID: "desktop", Surface: surface.KindCodex, Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(desktop); err != nil {
		t.Fatal(err)
	}
	desktop.Transport = "readOnly"
	if err := r.RegisterSession(desktop); err != nil {
		t.Fatal(err)
	}
	got, err = r.Session(desktop.ID)
	if err != nil || got.Transport != "readOnly" {
		t.Fatalf("desktop=%+v err=%v", got, err)
	}
}

func TestMigrationAddsCodexOwnershipWithoutLosingSessions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY, surface TEXT NOT NULL, name TEXT NOT NULL DEFAULT '', cwd TEXT NOT NULL DEFAULT '', pid INTEGER NOT NULL DEFAULT 0, status TEXT NOT NULL DEFAULT 'unknown', transcript TEXT NOT NULL DEFAULT '', has_local INTEGER NOT NULL DEFAULT 0, registered_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now'))); INSERT INTO sessions(id,surface,name) VALUES('old','codex','old thread')`)
	if closeErr := db.Close(); err != nil || closeErr != nil {
		t.Fatalf("seed err=%v close err=%v", err, closeErr)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	session, err := r.Session("old")
	if err != nil || session.Name != "old thread" || session.Source != "" || session.Transport != "" {
		t.Fatalf("session=%+v err=%v", session, err)
	}
}

func TestResolveTargetRejectsAmbiguityAndEscapesWildcards(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "same-a", "same-b", "literal%id")
	if _, err := r.ResolveTarget("same-"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity, got %v", err)
	}
	got, err := r.ResolveTarget("literal%")
	if err != nil || got != "literal%id" {
		t.Fatalf("got=%q err=%v", got, err)
	}
}

func TestAddRouteValidatesPatternAndCycles(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "a", "b", "c")
	if _, err := r.AddRoute("a", "a", ".*"); err == nil {
		t.Fatal("self route accepted")
	}
	if _, err := r.AddRoute("a", "b", "["); err == nil {
		t.Fatal("invalid regex accepted")
	}
	first, err := r.AddRoute("a", "b", ".*")
	if err != nil || first == 0 {
		t.Fatalf("first route: id=%d err=%v", first, err)
	}
	if _, err := r.AddRoute("b", "c", ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute("c", "a", ".*"); err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("cycle accepted: %v", err)
	}
}

func TestAddRouteConcurrentOppositeEdgesNeverCommitCycle(t *testing.T) {
	r := openTestRegistry(t)
	for iteration := 0; iteration < 200; iteration++ {
		from := fmt.Sprintf("concurrent-a-%d", iteration)
		to := fmt.Sprintf("concurrent-b-%d", iteration)
		register(t, r, from, to)
		start := make(chan struct{})
		errors := make(chan error, 2)
		var wg sync.WaitGroup
		for _, edge := range [][2]string{{from, to}, {to, from}} {
			wg.Add(1)
			go func(edge [2]string) {
				defer wg.Done()
				<-start
				_, err := r.AddRoute(edge[0], edge[1], ".*")
				errors <- err
			}(edge)
		}
		close(start)
		wg.Wait()
		close(errors)
		succeeded := 0
		rejectedCycle := 0
		for err := range errors {
			if err == nil {
				succeeded++
			} else if strings.Contains(err.Error(), "cycle") {
				rejectedCycle++
			} else {
				t.Fatalf("iteration %d unexpected error: %v", iteration, err)
			}
		}
		if succeeded != 1 || rejectedCycle != 1 {
			t.Fatalf("iteration %d succeeded=%d rejectedCycle=%d", iteration, succeeded, rejectedCycle)
		}
	}
}

func TestQueueLifecycleAndIdempotency(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	id, err := r.QueueMessageWithKey("s", "hello", "key-1")
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := r.QueueMessageWithKey("s", "ignored", "key-1")
	if err != nil || duplicate != id {
		t.Fatalf("duplicate=%d err=%v", duplicate, err)
	}
	now := time.Unix(100, 0)
	item, err := r.ClaimNextMessage("s", now)
	if err != nil || item == nil || item.Attempts != 1 || item.RelayHops != 0 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.NackMessage(item.ID, sql.ErrConnDone, now, 3); err != nil {
		t.Fatal(err)
	}
	if next, err := r.ClaimNextMessage("s", now); err != nil || next != nil {
		t.Fatalf("backoff not enforced: item=%+v err=%v", next, err)
	}
	item, err = r.ClaimNextMessage("s", now.Add(5*time.Second))
	if err != nil || item == nil || item.Attempts != 2 {
		t.Fatalf("retry=%+v err=%v", item, err)
	}
	if err := r.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	if count := r.QueueCount("s"); count != 0 {
		t.Fatalf("pending=%d", count)
	}
}

func TestRelayLineageSurvivesDeliveryUntilCompletion(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if _, err := r.QueueRelayMessage("s", "relay", "relay-key", 3); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", time.Now())
	if err != nil || item == nil || item.RelayHops != 3 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.AckMessageWithRelayHops(item.ID, "s", item.RelayHops); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveRuntimeState("s", surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState("s")
	if err != nil || !found || state.RelayHops != 3 {
		t.Fatalf("busy runtime=%+v found=%v err=%v", state, found, err)
	}
	if err := r.SaveRuntimeState("s", surface.TurnObservation{Status: surface.StatusIdle, CompletedTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	state, found, err = r.RuntimeState("s")
	if err != nil || !found || state.RelayHops != 0 {
		t.Fatalf("completed runtime=%+v found=%v err=%v", state, found, err)
	}
}

func TestRecoverInflightAndRuntimeState(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "hello"); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(200, 0)
	item, err := r.ClaimNextMessage("s", now)
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if n, err := r.RecoverInflight(now.Add(time.Second)); err != nil || n != 1 {
		t.Fatalf("recovered=%d err=%v", n, err)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "outcome is unknown") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	observation := surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1", CompletedTurnID: "turn-0"}
	if err := r.SaveRuntimeState("s", observation); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState("s")
	if err != nil || !found || state.ActiveTurnID != "turn-1" || state.CompletedTurnID != "turn-0" {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
}

func TestQueuePreservesLongMessageAndOptions(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	message := strings.Repeat("界", 100_000)
	if _, err := r.QueueMessageWithOptions("s", message, "", surface.SendOptions{Model: "sonnet"}); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", time.Now())
	if err != nil || item == nil || item.Message != message || item.Model != "sonnet" {
		t.Fatalf("item length=%d model=%q err=%v", len(item.Message), item.Model, err)
	}
}

func TestQueueClaimIsConcurrentAndFIFOAcrossBackoff(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "first"); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage("s", "second"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	var wg sync.WaitGroup
	claimed := make(chan *QueuedMessage, 10)
	errors := make(chan error, 10)
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			item, err := r.ClaimNextMessage("s", now)
			if err != nil {
				errors <- err
				return
			}
			if item != nil {
				claimed <- item
			}
		}()
	}
	wg.Wait()
	close(claimed)
	close(errors)
	if len(errors) != 0 || len(claimed) != 1 {
		t.Fatalf("claims=%d errors=%d", len(claimed), len(errors))
	}
	first := <-claimed
	if first.Message != "first" {
		t.Fatalf("claimed=%q", first.Message)
	}
	if err := r.NackMessage(first.ID, sql.ErrConnDone, now, 3); err != nil {
		t.Fatal(err)
	}
	if item, err := r.ClaimNextMessage("s", now); err != nil || item != nil {
		t.Fatalf("second overtook backed-off first: item=%+v err=%v", item, err)
	}
}

func TestQueueDeadLetterAndExplicitRetry(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "fail"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for attempt := 1; attempt <= 3; attempt++ {
		item, err := r.ClaimNextMessage("s", now)
		if err != nil || item == nil {
			t.Fatalf("attempt=%d item=%+v err=%v", attempt, item, err)
		}
		if err := r.NackMessage(item.ID, sql.ErrConnDone, now, 3); err != nil {
			t.Fatal(err)
		}
		now = now.Add(time.Minute)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || rows[0].Attempts != 3 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := r.RetryMessage(rows[0].ID); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", now)
	if err != nil || item == nil || item.Attempts != 1 {
		t.Fatalf("retried=%+v err=%v", item, err)
	}
}

func TestAttentionItemsTrackDeadLetterResolutionWithoutStatusInference(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "needs a decision"); err != nil {
		t.Fatal(err)
	}
	if items, err := r.ListAttentionItems(false); err != nil || len(items) != 0 {
		t.Fatalf("pending attention=%+v err=%v", items, err)
	}
	now := time.Now()
	item, err := r.ClaimNextMessage("s", now)
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.NackMessage(item.ID, sql.ErrConnDone, now, 2); err != nil {
		t.Fatal(err)
	}
	if items, err := r.ListAttentionItems(false); err != nil || len(items) != 0 {
		t.Fatalf("retryable attention=%+v err=%v", items, err)
	}
	item, err = r.ClaimNextMessage("s", now.Add(time.Minute))
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.NackMessage(item.ID, sql.ErrConnDone, now.Add(time.Minute), 2); err != nil {
		t.Fatal(err)
	}
	items, err := r.ListAttentionItems(false)
	if err != nil || len(items) != 1 || items[0].SessionID != "s" || items[0].QueueID != item.ID || items[0].ResolvedAt != "" {
		t.Fatalf("dead attention=%+v err=%v", items, err)
	}
	if err := r.RetryMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	if open, err := r.ListAttentionItems(false); err != nil || len(open) != 0 {
		t.Fatalf("open attention=%+v err=%v", open, err)
	}
	all, err := r.ListAttentionItems(true)
	if err != nil || len(all) != 1 || all[0].Resolution != "retrying" || all[0].ResolvedAt == "" {
		t.Fatalf("resolved attention=%+v err=%v", all, err)
	}
}

func TestAttentionItemResolvesWhenDeadLetterIsCanceled(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "cancel this"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterUnknown(item.ID, errors.New("uncertain")); err != nil {
		t.Fatal(err)
	}
	items, err := r.ListAttentionItems(false)
	if err != nil || len(items) != 1 || items[0].Reason != "Delivery outcome could not be confirmed" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := r.CancelMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	all, err := r.ListAttentionItems(true)
	if err != nil || len(all) != 1 || all[0].Resolution != "canceled" || all[0].ResolvedAt == "" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
}

func TestQueueCancelRemovesPendingDelivery(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "cancel me"); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := r.CancelMessage(rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if visible, err := r.ListQueue(false); err != nil || len(visible) != 0 {
		t.Fatalf("visible=%+v err=%v", visible, err)
	}
	if item, err := r.ClaimNextMessage("s", time.Now()); err != nil || item != nil {
		t.Fatalf("canceled item claimed: item=%+v err=%v", item, err)
	}
}

func TestDeliveryHistoryIsBoundedAndFilterable(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "writer", "reviewer")
	long := strings.Repeat("x", maxHistoryText+100)
	if err := r.RecordHistory(HistoryEntry{Kind: "sent", SessionID: "writer", Message: long}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordHistory(HistoryEntry{Kind: "relay", SessionID: "reviewer", SourceSessionID: "writer", Message: "handoff"}); err != nil {
		t.Fatal(err)
	}
	entries, err := r.ListHistory(10, "writer")
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries=%+v err=%v", entries, err)
	}
	if len(entries[1].Message) != maxHistoryText+len("\n[truncated]") {
		t.Fatalf("history message length=%d", len(entries[1].Message))
	}
}

func TestListHistoryPageUsesStableCursor(t *testing.T) {
	r := openTestRegistry(t)
	for i := 0; i < 5; i++ {
		if err := r.RecordHistory(HistoryEntry{Kind: fmt.Sprintf("event_%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	first, hasMore, err := r.ListHistoryPage(2, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 2 || !hasMore || first[0].ID <= first[1].ID {
		t.Fatalf("first page=%v hasMore=%v", first, hasMore)
	}
	second, hasMore, err := r.ListHistoryPage(2, first[len(first)-1].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 2 || !hasMore || second[0].ID >= first[len(first)-1].ID {
		t.Fatalf("second page=%v hasMore=%v", second, hasMore)
	}
	last, hasMore, err := r.ListHistoryPage(2, second[len(second)-1].ID, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(last) != 1 || hasMore {
		t.Fatalf("last page=%v hasMore=%v", last, hasMore)
	}
}

func TestListHistoryPageFiltersAcrossAllRows(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "named-session", Surface: surface.KindCodex, Name: "Release Writer"}); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("writer", "named-session"); err != nil {
		t.Fatal(err)
	}
	entries := []HistoryEntry{
		{Kind: "sent", Message: "ordinary update"},
		{Kind: "failed", Message: "deploy alpha"},
		{Kind: "sent", Message: "deploy beta"},
		{Kind: "sent", SessionID: "named-session", Message: "handoff"},
	}
	for _, entry := range entries {
		if err := r.RecordHistory(entry); err != nil {
			t.Fatal(err)
		}
	}
	filtered, hasMore, err := r.ListHistoryPage(25, 0, "sent", "deploy")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Message != "deploy beta" || hasMore {
		t.Fatalf("filtered=%v hasMore=%v", filtered, hasMore)
	}
	kinds, err := r.ListHistoryKinds()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(kinds, ",") != "failed,sent" {
		t.Fatalf("kinds=%v", kinds)
	}
	for _, query := range []string{"@writer", "codex/Release Writer"} {
		matched, _, err := r.ListHistoryPage(25, 0, "", query)
		if err != nil {
			t.Fatal(err)
		}
		if len(matched) != 1 || matched[0].SessionID != "named-session" {
			t.Fatalf("query=%q matched=%v", query, matched)
		}
	}
}

func TestCancelMessagesRecordsAuditEntries(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "writer")
	if err := r.QueueMessage("writer", "one"); err != nil {
		t.Fatal(err)
	}
	if err := r.QueueMessage("writer", "two"); err != nil {
		t.Fatal(err)
	}
	count, err := r.CancelMessagesForSession("writer")
	if err != nil || count != 2 {
		t.Fatalf("count=%d err=%v", count, err)
	}
	entries, err := r.ListHistory(10, "writer")
	if err != nil {
		t.Fatal(err)
	}
	canceled := 0
	for _, entry := range entries {
		if entry.Kind == "canceled" {
			canceled++
		}
	}
	if canceled != 2 {
		t.Fatalf("canceled history=%d entries=%+v", canceled, entries)
	}
}

func TestCancelMessageRemovesDeadLetter(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "writer")
	if err := r.QueueMessage("writer", "dead"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("writer", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.NackMessage(item.ID, errors.New("failed"), time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if err := r.CancelMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(true)
	if err != nil || len(rows) != 1 || rows[0].Status != "canceled" {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestClaimDeadLettersStaleInflightWithoutAutomaticRedelivery(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "recover"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	first, err := r.ClaimNextMessage("s", now)
	if err != nil || first == nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	if item, err := r.ClaimNextMessage("s", now.Add(30*time.Second)); err != nil || item != nil {
		t.Fatalf("fresh inflight reclaimed: item=%+v err=%v", item, err)
	}
	recovered, err := r.ClaimNextMessage("s", now.Add(2*time.Minute))
	if err != nil || recovered != nil {
		t.Fatalf("recovered=%+v err=%v", recovered, err)
	}
	rows, err := r.ListQueue(false)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || !strings.Contains(rows[0].LastError, "retry explicitly") {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

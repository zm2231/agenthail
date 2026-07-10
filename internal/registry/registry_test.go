package registry

import (
	"database/sql"
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

func TestSessionReturnsCompleteRegisteredSnapshot(t *testing.T) {
	r := openTestRegistry(t)
	want := surface.Session{ID: "full", Surface: surface.KindClaude, Name: "writer", Cwd: "/tmp/project", PID: 42, Status: surface.StatusBusy, Transcript: "/tmp/thread.jsonl", HasLocal: true}
	if err := r.RegisterSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Surface != want.Surface || got.Name != want.Name || got.Cwd != want.Cwd || got.PID != want.PID || got.Status != want.Status || got.Transcript != want.Transcript || got.HasLocal != want.HasLocal {
		t.Fatalf("session=%+v want=%+v", got, want)
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
	if err != nil || item == nil || item.Attempts != 1 {
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

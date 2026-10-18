package registry

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
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

func TestOpenRestrictsDefaultStateDirectoryCreatedByAnotherWriter(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	state := filepath.Join(home, ".agenthail")
	if err := os.Mkdir(state, 0755); err != nil {
		t.Fatal(err)
	}
	r, err := Open("")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	info, err := os.Stat(state)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0700 {
		t.Fatalf("state directory mode=%o, want 700", mode)
	}
}

func TestOpenUsesWALAndAllowsReadsDuringAWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Close()
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	register(t, first, "session")
	var mode string
	if err := first.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || !strings.EqualFold(mode, "wal") {
		t.Fatalf("journal mode=%q err=%v", mode, err)
	}
	tx, err := first.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE sessions SET name='held' WHERE id='session'`); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, _, _, err := second.GetSession("session")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("read blocked behind an external writer")
	}
}

func TestOpenRunsMigrationsOnlyOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	first, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.db.Exec(`CREATE TRIGGER reject_queue_updates BEFORE UPDATE ON message_queue BEGIN SELECT RAISE(FAIL, 'unexpected migration write'); END`); err != nil {
		first.Close()
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	var version int
	if err := second.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("version=%d err=%v", version, err)
	}
}

func TestQueueCompactIsTypedAndDeduplicated(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	first, err := r.QueueCompact("session")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.QueueCompact("session")
	if err != nil || second != first || r.QueueCount("session") != 1 {
		t.Fatalf("first=%d second=%d count=%d err=%v", first, second, r.QueueCount("session"), err)
	}
	item, err := r.ClaimNextMessage("session", time.Now())
	if err != nil || item == nil || item.Operation != QueueOperationCompact || item.Message != "/compact" {
		t.Fatalf("item=%+v err=%v", item, err)
	}
}

func TestDeferMessageNeverDeadLettersPreDeliveryFailures(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "session")
	if err := r.QueueMessage("session", "wait"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	for attempt := 0; attempt < 7; attempt++ {
		item, err := r.ClaimNextMessage("session", now)
		if err != nil || item == nil {
			t.Fatalf("attempt=%d item=%+v err=%v", attempt, item, err)
		}
		if err := r.DeferMessage(item.ID, errors.New("delivery did not start"), now); err != nil {
			t.Fatal(err)
		}
		now = now.Add(6 * time.Minute)
	}
	item, err := r.QueueItem(1, nil)
	if err != nil || item.Status != "pending" || item.Attempts != 0 {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	for attempt := 1; attempt < 3; attempt++ {
		claimed, err := r.ClaimNextMessage("session", now)
		if err != nil || claimed == nil || claimed.Attempts != attempt {
			t.Fatalf("attempt=%d claimed=%+v err=%v", attempt, claimed, err)
		}
		if err := r.NackMessage(claimed.ID, errors.New("transport closed"), now, 3); err != nil {
			t.Fatal(err)
		}
		now = now.Add(6 * time.Minute)
	}
	if item, err := r.QueueItem(1, nil); err != nil || item.Status != "pending" {
		t.Fatalf("real errors after deferrals spent the budget early: item=%+v err=%v", item, err)
	}
}

func register(t *testing.T, r *Registry, ids ...string) {
	t.Helper()
	for _, id := range ids {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAddToChannelRejectsMissingChannel(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.AddToChannel("missing", "s"); err == nil {
		t.Fatal("missing channel accepted")
	}
}

func TestSetAliasKeepsOneNamePerSession(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "session-alias", Surface: surface.KindClaude, Name: "Alias test"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("first", session.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("second", session.ID); err != nil {
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

func TestReserveGeneratedAliasKeepsExistingHandleAndNeverSteals(t *testing.T) {
	r := openTestRegistry(t)
	for _, id := range []string{"one", "two"} {
		if err := r.RegisterSession(surface.Session{ID: id, Surface: surface.KindCodex}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := r.ReserveGeneratedAlias("one", "Build Agent")
	if err != nil || first != "build-agent" {
		t.Fatalf("first=%q err=%v", first, err)
	}
	again, err := r.ReserveGeneratedAlias("one", "other")
	if err != nil || again != first {
		t.Fatalf("again=%q err=%v", again, err)
	}
	second, err := r.ReserveGeneratedAlias("two", "Build Agent")
	if err != nil || second == first {
		t.Fatalf("second=%q err=%v", second, err)
	}
	owner, err := r.LookupAlias(first)
	if err != nil || owner != "one" {
		t.Fatalf("owner=%q err=%v", owner, err)
	}
}

func TestSessionReturnsCompleteRegisteredSnapshot(t *testing.T) {
	r := openTestRegistry(t)
	want := surface.Session{ID: "full", Surface: surface.KindClaude, Name: "writer", Cwd: "/tmp/project", PID: 42, Status: surface.StatusBusy, Transcript: "/tmp/thread.jsonl", HasLocal: true, Source: "vscode", Transport: "desktop", ConfiguredModel: "claude-opus-5-5[1m]", LastActive: time.UnixMilli(1784424580603), Runtime: &surface.Runtime{Launcher: surface.LauncherClaudeBG}}
	if err := r.RegisterSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(want.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Surface != want.Surface || got.Name != want.Name || got.Cwd != want.Cwd || got.PID != want.PID || got.Status != want.Status || got.Transcript != want.Transcript || got.HasLocal != want.HasLocal || got.Source != want.Source || got.Transport != want.Transport || got.ConfiguredModel != want.ConfiguredModel || !got.LastActive.Equal(want.LastActive) || got.Runtime == nil || got.Runtime.Launcher != want.Runtime.Launcher {
		t.Fatalf("session=%+v want=%+v", got, want)
	}
}

func TestRegisterSessionUpdatesManagedCodexTransportFromDesktopDiscovery(t *testing.T) {
	r := openTestRegistry(t)
	managed := surface.Session{ID: "managed", Surface: surface.KindCodex, Name: "Started here", Status: surface.StatusBusy, Source: "agenthail", Transport: "managed"}
	if err := r.RegisterSession(managed); err != nil {
		t.Fatal(err)
	}
	discovered := managed
	discovered.Source = "vscode"
	discovered.Transport = "readOnly"
	if err := r.RegisterSession(discovered); err != nil {
		t.Fatal(err)
	}
	got, err := r.Session(managed.ID)
	if err != nil || got.Source != "agenthail" || got.Transport != "managed" {
		t.Fatalf("session=%+v err=%v", got, err)
	}
	discovered = managed
	discovered.Name = "Updated title"
	discovered.Status = surface.SessionStatus("notLoaded")
	discovered.Source = "vscode"
	discovered.Transport = "desktop"
	if err := r.RegisterSession(discovered); err != nil {
		t.Fatal(err)
	}
	got, err = r.Session(managed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Source != "vscode" || got.Transport != "desktop" || got.Name != "Updated title" || got.Status != surface.SessionStatus("notLoaded") {
		t.Fatalf("session=%+v", got)
	}
	desktop := surface.Session{ID: "desktop", Surface: surface.KindCodex, Source: "vscode", Transport: "desktop"}
	if err := r.RegisterSession(desktop); err != nil {
		t.Fatal(err)
	}
	desktop.Source = "agenthail"
	desktop.Transport = "managed"
	if err := r.RegisterSession(desktop); err != nil {
		t.Fatal(err)
	}
	got, err = r.Session(desktop.ID)
	if err != nil || got.Source != "vscode" || got.Transport != "desktop" {
		t.Fatalf("desktop=%+v err=%v", got, err)
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

func TestResolveSurfaceTargetMatchesOnlyThatSurface(t *testing.T) {
	r := openTestRegistry(t)
	for _, session := range []surface.Session{
		{ID: "019f-codex", Surface: surface.KindCodex, Name: "release notes"},
		{ID: "019f-claude", Surface: surface.KindClaude, Name: "release notes"},
	} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetAlias("writer", "019f-claude"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"019f", "019f-codex", "release"} {
		if got, err := r.ResolveSurfaceTarget(surface.KindCodex, target); err != nil || got != "019f-codex" {
			t.Fatalf("%s: got=%q err=%v", target, got, err)
		}
	}
	if _, err := r.ResolveSurfaceTarget(surface.KindCodex, "writer"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("a Claude alias resolved for Codex: %v", err)
	}
	if _, err := r.ResolveTarget("019f"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("unscoped resolution must stay ambiguous across surfaces: %v", err)
	}
	if err := r.RegisterSession(surface.Session{ID: "019f-codex-2", Surface: surface.KindCodex}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ResolveSurfaceTarget(surface.KindCodex, "019f"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("expected ambiguity among Codex sessions, got %v", err)
	}
}

func TestResolveTargetAcceptsHandleSpelling(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "builder-session")
	if err := r.SetAlias("builder", "builder-session"); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"builder", "@builder"} {
		got, err := r.ResolveTarget(target)
		if err != nil || got != "builder-session" {
			t.Fatalf("%s: got=%q err=%v", target, got, err)
		}
	}
	if got, err := r.ResolveTarget("@Builder"); err == nil && got == "builder-session" {
		t.Fatalf("handle lookup must be case-sensitive, got %q", got)
	}
}

func TestSearchSessionsMatchesStoredNameCwdAndAlias(t *testing.T) {
	r := openTestRegistry(t)
	session := surface.Session{ID: "old", Surface: surface.KindCodex, Name: "Quarterly planning", Cwd: "/work/agenthail"}
	if err := r.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	if err := r.SetAlias("planner", session.ID); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"quarterly", "agenthail", "planner"} {
		results, err := r.SearchSessions(surface.KindCodex, query, 20)
		if err != nil || len(results) != 1 || results[0].ID != session.ID {
			t.Fatalf("query=%q results=%+v err=%v", query, results, err)
		}
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

func TestListRoutesIncludesDerivedFiringEvidence(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "from", "first", "second")
	first, err := r.AddRoute("from", "first", ".*")
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.AddRoute("from", "second", ".*")
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := r.RecordRelayDelivery(first, "turn-one"); err != nil || !reserved {
		t.Fatalf("first record reserved=%v err=%v", reserved, err)
	}
	if reserved, err := r.RecordRelayDelivery(first, "turn-two"); err != nil || !reserved {
		t.Fatalf("second record reserved=%v err=%v", reserved, err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 2 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if routes[0].ID != first || routes[0].FireCount != 2 || routes[0].LastFiredAt == "" {
		t.Fatalf("first route=%+v", routes[0])
	}
	if routes[1].ID != second || routes[1].FireCount != 0 || routes[1].LastFiredAt != "" {
		t.Fatalf("second route=%+v", routes[1])
	}
}

func TestOneShotRouteRetainsEvidenceAfterDeactivation(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "from", "to")
	id, err := r.AddRouteWithOptions("from", "to", ".*", true)
	if err != nil {
		t.Fatal(err)
	}
	if reserved, err := r.RecordRelayDelivery(id, "turn-one"); err != nil || !reserved {
		t.Fatalf("reserved=%v err=%v", reserved, err)
	}
	if err := r.DeactivateRoute(id); err != nil {
		t.Fatal(err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if !routes[0].Once || routes[0].Active || routes[0].FireCount != 1 || routes[0].LastFiredAt == "" {
		t.Fatalf("route=%+v", routes[0])
	}
	watched, err := r.WatchedSessions()
	if err != nil || len(watched) != 0 {
		t.Fatalf("watched=%+v err=%v", watched, err)
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

func TestOldQueuedMessageStaysPendingAndClaimable(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	id, err := r.QueueMessageWithKey("s", "old", "old")
	if err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(72 * time.Hour)
	r.now = func() time.Time { return later }
	watched, err := r.WatchedSessions()
	if err != nil || len(watched) != 1 {
		t.Fatalf("watched=%+v err=%v", watched, err)
	}
	item, err := r.ClaimNextMessage("s", later)
	if err != nil || item == nil || item.ID != id {
		t.Fatalf("claim=%+v err=%v", item, err)
	}
}

func TestDeadDeliveryMovesToHistoryAfterReviewWindow(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if _, err := r.QueueMessageWithKey("s", "uncertain old delivery", "old-unknown"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.DeadLetterUnknown(item.ID, errors.New("daemon stopped")); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].Historical {
		t.Fatalf("fresh dead rows=%+v err=%v", rows, err)
	}
	afterTTL := time.Now().Add(2 * deadReviewWindow)
	r.now = func() time.Time { return afterTTL }
	if err := r.ReconcileAttentionItems(afterTTL); err != nil {
		t.Fatal(err)
	}
	rows, err = r.ListQueue(false, nil)
	if err != nil || len(rows) != 0 {
		t.Fatalf("current rows=%+v err=%v", rows, err)
	}
	rows, err = r.ListQueue(true, nil)
	if err != nil || len(rows) != 1 || !rows[0].Historical || rows[0].Evidence != surface.EvidenceUnknown {
		t.Fatalf("history rows=%+v err=%v", rows, err)
	}
	attention, err := r.ListAttentionItems(false)
	if err != nil || len(attention) != 0 {
		t.Fatalf("open attention=%+v err=%v", attention, err)
	}
	allAttention, err := r.ListAttentionItems(true)
	if err != nil || len(allAttention) != 1 || allAttention[0].Resolution != "history" || allAttention[0].ResolvedAt == "" {
		t.Fatalf("historical attention=%+v err=%v", allAttention, err)
	}
}

func TestInflightRemainsCurrentUntilFinalized(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "still sending"); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	item, err := r.ClaimNextMessage("s", now)
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	r.now = func() time.Time { return now.Add(2 * deadReviewWindow) }
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].Status != "inflight" || rows[0].Historical {
		t.Fatalf("current rows=%+v err=%v", rows, err)
	}
	if got := r.QueueCount("s"); got != 1 {
		t.Fatalf("queue count=%d", got)
	}
}

func TestQueueRetainsTransportAcceptanceEvidence(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "peer delivery"); err != nil {
		t.Fatal(err)
	}
	item, err := r.ClaimNextMessage("s", time.Now())
	if err != nil || item == nil {
		t.Fatalf("item=%+v err=%v", item, err)
	}
	if err := r.AckMessageWithEvidence(item.ID, "s", 0, surface.EvidenceTransportAccepted, ""); err != nil {
		t.Fatal(err)
	}
	row, err := r.QueueItem(item.ID, nil)
	if err != nil || row.Evidence != surface.EvidenceTransportAccepted {
		t.Fatalf("row=%+v err=%v", row, err)
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

func TestSaveRuntimeStatePreservesDeliveryNotificationArm(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.MarkDeliveryStarted("s", "turn-1", ""); err != nil {
		t.Fatal(err)
	}
	if err := r.SaveRuntimeState("s", surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState("s")
	if err != nil || !found || !state.NotificationArmed {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
	}
	if err := r.ClearCompletionNotification("s"); err != nil {
		t.Fatal(err)
	}
	state, found, err = r.RuntimeState("s")
	if err != nil || !found || state.NotificationArmed {
		t.Fatalf("state=%+v found=%v err=%v", state, found, err)
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
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || rows[0].Evidence != surface.EvidenceUnknown {
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
	rows, err := r.ListQueue(false, nil)
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
	if err != nil || len(items) != 1 || items[0].Reason == "" || items[0].RequestedAction == "" {
		t.Fatalf("items=%+v err=%v", items, err)
	}
	if err := r.CancelMessage(item.ID); err != nil {
		t.Fatal(err)
	}
	all, err := r.ListAttentionItems(true)
	if err != nil || len(all) != 1 || all[0].Resolution != "canceled" || all[0].ResolvedAt == "" {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	if row, err := r.QueueItem(item.ID, nil); err != nil || row.Status != "canceled" {
		t.Fatalf("canceled row=%+v err=%v", row, err)
	}
}

func TestAttentionItemsExplainTerminalDeliveryRecovery(t *testing.T) {
	attention := func(t *testing.T, cause string) AttentionItem {
		t.Helper()
		r := openTestRegistry(t)
		register(t, r, "s")
		if err := r.QueueMessage("s", "needs recovery"); err != nil {
			t.Fatal(err)
		}
		item, err := r.ClaimNextMessage("s", time.Now())
		if err != nil || item == nil {
			t.Fatalf("item=%+v err=%v", item, err)
		}
		if _, err := r.DeadLetterMessage(item.ID, errors.New(cause)); err != nil {
			t.Fatal(err)
		}
		items, err := r.ListAttentionItems(false)
		if err != nil || len(items) != 1 || items[0].Reason == "" || items[0].RequestedAction == "" {
			t.Fatalf("items=%+v err=%v", items, err)
		}
		return items[0]
	}
	generic := attention(t, "transport closed")
	reasons := map[string]string{}
	for _, cause := range []string{
		"delivery rejected [target missing]: HTTP 404",
		"delivery rejected [authentication needed]: HTTP 401",
		"delivery rejected [access denied]: HTTP 403",
		"delivery rejected [invalid request]: HTTP 400",
	} {
		item := attention(t, cause)
		if item.Reason == generic.Reason || item.RequestedAction == generic.RequestedAction {
			t.Fatalf("%q fell back to the generic explanation %+v", cause, item)
		}
		if other, seen := reasons[item.Reason]; seen {
			t.Fatalf("%q and %q share reason %q", cause, other, item.Reason)
		}
		reasons[item.Reason] = cause
	}
}

func TestQueueSnapshotsAndBackgroundScanKeepOldPendingDelivery(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s", "sender")
	id, deliveryID, err := r.QueueDeliveryWithIntent("s", "old snapshot", "old-snapshot", surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	afterTTL := time.Now().Add(48 * time.Hour)
	r.now = func() time.Time { return afterTTL }
	beforeHistory, err := r.ListHistory(20, "")
	if err != nil {
		t.Fatal(err)
	}
	beforeRow, err := r.QueueItem(id, nil)
	if err != nil || beforeRow.Status != "pending" {
		t.Fatalf("before row=%+v err=%v", beforeRow, err)
	}
	beforeDelivery, err := r.DeliveryIntent(deliveryID)
	if err != nil {
		t.Fatal(err)
	}
	if rows, err := r.ListQueue(false, nil); err != nil || len(rows) != 1 || rows[0].Status != "pending" {
		t.Fatalf("old current snapshot=%+v err=%v", rows, err)
	}
	if items, err := r.ListAttentionItems(false); err != nil || len(items) != 0 {
		t.Fatalf("attention snapshot=%+v err=%v", items, err)
	}
	afterRow, err := r.QueueItem(id, nil)
	if err != nil || afterRow.Status != "pending" {
		t.Fatalf("after row=%+v err=%v", afterRow, err)
	}
	afterDelivery, err := r.DeliveryIntent(deliveryID)
	if err != nil || afterDelivery.Status != beforeDelivery.Status || afterDelivery.Failure != beforeDelivery.Failure || afterDelivery.NotificationQueueID != beforeDelivery.NotificationQueueID {
		t.Fatalf("delivery intent changed before=%+v after=%+v err=%v", beforeDelivery, afterDelivery, err)
	}
	afterHistory, err := r.ListHistory(20, "")
	if err != nil || len(afterHistory) != len(beforeHistory) {
		t.Fatalf("history changed before=%+v after=%+v err=%v", beforeHistory, afterHistory, err)
	}
	if err := r.ReconcileAttentionItems(afterTTL); err != nil {
		t.Fatal(err)
	}
	scanned, err := r.QueueItem(id, nil)
	if err != nil || scanned.Status != "pending" {
		t.Fatalf("background scan row=%+v err=%v", scanned, err)
	}
	history, err := r.ListHistory(20, "")
	if err != nil || len(history) != len(beforeHistory) {
		t.Fatalf("background scan history=%+v err=%v", history, err)
	}
}

func TestQueueCancelRemovesPendingDelivery(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "s")
	if err := r.QueueMessage("s", "cancel me"); err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
	if err := r.CancelMessage(rows[0].ID); err != nil {
		t.Fatal(err)
	}
	if visible, err := r.ListQueue(false, nil); err != nil || len(visible) != 0 {
		t.Fatalf("visible=%+v err=%v", visible, err)
	}
	if item, err := r.ClaimNextMessage("s", time.Now()); err != nil || item != nil {
		t.Fatalf("canceled item claimed: item=%+v err=%v", item, err)
	}
}

func TestSteerClaimSkipsOlderExplicitQueueWithoutReorderingIt(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindCodex, Transport: "managed", Status: surface.StatusBusy}); err != nil {
		t.Fatal(err)
	}
	queueID, err := r.QueueMessageWithOptions("target", "explicit queue", "", surface.SendOptions{BusyDelivery: "queue"})
	if err != nil {
		t.Fatal(err)
	}
	steerID, err := r.QueueMessageWithOptions("target", "busy steer", "", surface.SendOptions{BusyDelivery: "steer"})
	if err != nil {
		t.Fatal(err)
	}
	steer, err := r.ClaimNextSteerMessage("target", time.Now())
	if err != nil || steer == nil || steer.ID != steerID {
		t.Fatalf("steer=%+v err=%v", steer, err)
	}
	if err := r.AckMessage(steer.ID); err != nil {
		t.Fatal(err)
	}
	queue, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || queue == nil || queue.ID != queueID || queue.Message != "explicit queue" {
		t.Fatalf("queue=%+v err=%v", queue, err)
	}
}

func TestSteerQueueNormalizesOptionsAndExcludesThemFromSteerClaims(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindCodex, Transport: "managed", Status: surface.StatusBusy}); err != nil {
		t.Fatal(err)
	}
	id, err := r.QueueRelayMessageWithOptions("target", "optioned steer", "relay:options", 1, surface.SendOptions{Model: "model-a", BusyDelivery: "steer", TurnOptions: surface.TurnOptions{Effort: "high"}})
	if err != nil {
		t.Fatal(err)
	}
	item, err := r.QueueItem(id, nil)
	if err != nil {
		t.Fatal(err)
	}
	if item.BusyDelivery != "queue" || item.Model != "model-a" || item.Effort != "high" {
		t.Fatalf("normalized queue item=%+v", item)
	}
	steer, err := r.ClaimNextSteerMessage("target", time.Now())
	if err != nil || steer != nil {
		t.Fatalf("option-bearing item was steer-claimable: item=%+v err=%v", steer, err)
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
	rows, err := r.ListQueue(false, nil)
	if err != nil || len(rows) != 1 || rows[0].Status != "dead" || rows[0].Evidence != surface.EvidenceUnknown {
		t.Fatalf("rows=%+v err=%v", rows, err)
	}
}

func TestReadsDoNotWaitForAWriterBlockedOnAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	register(t, r, "session")
	if err := r.EnsureCatalogState(); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	held, err := other.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := held.Exec(`UPDATE sessions SET name='held by another process' WHERE id='session'`); err != nil {
		t.Fatal(err)
	}
	writeDone := make(chan error, 1)
	go func() {
		writeDone <- r.RegisterSession(surface.Session{ID: "waiting-writer", Surface: surface.KindCodex})
	}()
	for deadline := time.Now().Add(2 * time.Second); r.db.Stats().InUse == 0; {
		if time.Now().After(deadline) {
			t.Fatal("writer never took the write connection")
		}
		time.Sleep(time.Millisecond)
	}
	readsDone := make(chan error, 1)
	go func() {
		if _, err := r.Session("session"); err != nil {
			readsDone <- err
			return
		}
		if _, _, err := r.CatalogState(); err != nil {
			readsDone <- err
			return
		}
		if _, err := r.QueueCounts(); err != nil {
			readsDone <- err
			return
		}
		if _, err := r.ListHistory(10, ""); err != nil {
			readsDone <- err
			return
		}
		if _, err := r.ReadSessionJournalPage("session", 0, 10); err != nil {
			readsDone <- err
			return
		}
		readsDone <- r.EnsureAliasAvailable("unclaimed")
	}()
	select {
	case err := <-readsDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("reads waited for a writer blocked on another process's lock")
	}
	if err := held.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
}

func TestRecordWaitingAnnouncesEachQuestionUntilMarked(t *testing.T) {
	r := openTestRegistry(t)
	register(t, r, "asking")
	question := &surface.Waiting{Kind: surface.WaitingQuestion, ID: "conversation:question:1"}
	first := time.Date(2026, 10, 18, 8, 14, 41, 0, time.UTC)
	record := func(waiting *surface.Waiting, now time.Time) WaitingRecord {
		t.Helper()
		result, err := r.RecordWaiting("asking", waiting, now)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	if got := record(question, first); !got.Announce || !got.Changed || got.Waiting == nil || !got.Waiting.Since.Equal(first) {
		t.Fatalf("new question=%+v", got)
	}
	if got := record(question, first.Add(time.Hour)); !got.Announce || got.Changed || !got.Waiting.Since.Equal(first) {
		t.Fatalf("an unannounced question must stay due with its first-seen time and unchanged: %+v", got)
	}
	if err := r.MarkWaitingAnnounced("asking", "conversation:question:2"); err != nil {
		t.Fatal(err)
	}
	if !record(question, first).Announce {
		t.Fatal("marking another question announced this one")
	}
	if err := r.MarkWaitingAnnounced("asking", question.ID); err != nil {
		t.Fatal(err)
	}
	if record(question, first).Announce {
		t.Fatal("an announced question is due again")
	}
	if got := record(nil, first); got.Waiting != nil || got.Announce || !got.Changed {
		t.Fatalf("clear=%+v", got)
	}
	if got := record(nil, first); got.Changed {
		t.Fatalf("clearing nothing changed the record: %+v", got)
	}
	if !record(question, first).Announce {
		t.Fatal("the same question asked again after it cleared is not due")
	}
}

func TestQueueHoldFollowsTheDrainRules(t *testing.T) {
	r := openTestRegistry(t)
	if err := r.RegisterSession(surface.Session{ID: "target", Surface: surface.KindCodex, Transport: "app-server"}); err != nil {
		t.Fatal(err)
	}
	steers := SteerSurfaces{surface.KindCodex: true}
	if err := r.SaveRuntimeState("target", surface.TurnObservation{Status: surface.StatusBusy, ActiveTurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	queue := func(message, busyDelivery string) int64 {
		t.Helper()
		id, err := r.QueueMessageWithOptions("target", message, "", surface.SendOptions{BusyDelivery: busyDelivery})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	queued, steer, nextSteer := queue("after the turn", ""), queue("into the turn", "steer"), queue("also into the turn", "steer")
	held := func(id int64, steer SteerSurfaces) QueueHold {
		t.Helper()
		item, err := r.QueueItem(id, steer)
		if err != nil {
			t.Fatal(err)
		}
		return item.HeldBy
	}
	// Empty must mean the next claim of that kind takes exactly that row.
	claimable := func(claim func(string, time.Time) (*QueuedMessage, error), id int64) {
		t.Helper()
		item, err := claim("target", time.Now())
		if err != nil || item == nil || item.ID != id {
			t.Fatalf("claim=%+v err=%v, want %d", item, err, id)
		}
	}
	if held(queued, steers) != QueueHeldBusy || held(steer, steers) != "" || held(nextSteer, steers) != QueueHeldBehind {
		t.Fatalf("busy turn: queued=%q steer=%q next=%q, want busy, nothing, behind", held(queued, steers), held(steer, steers), held(nextSteer, steers))
	}
	if held(steer, nil) != QueueHeldBusy || held(steer, SteerSurfaces{surface.KindClaude: true}) != QueueHeldBusy {
		t.Fatalf("a target that cannot steer reported steer=%q", held(steer, nil))
	}
	if _, err := r.RecordWaiting("target", &surface.Waiting{Kind: surface.WaitingApproval, ID: "thread:approval"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if held(queued, steers) != QueueHeldWaiting || held(steer, steers) != QueueHeldWaiting {
		t.Fatalf("waiting turn: queued=%q steer=%q", held(queued, steers), held(steer, steers))
	}
	if _, err := r.RecordWaiting("target", nil, time.Now()); err != nil {
		t.Fatal(err)
	}
	claimable(r.ClaimNextSteerMessage, steer)
	if err := r.DeferMessage(steer, errors.New("target refused the steer"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if held(steer, steers) != QueueHeldRetrying || held(nextSteer, steers) != QueueHeldBehind {
		t.Fatalf("deferred steer=%q next=%q, want retrying and behind", held(steer, steers), held(nextSteer, steers))
	}
	if item, err := r.ClaimNextSteerMessage("target", time.Now()); err != nil || item != nil {
		t.Fatalf("a steer behind a deferred one was claimable: %+v err=%v", item, err)
	}
	if err := r.SaveRuntimeState("target", surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	if held(queued, steers) != "" || held(steer, steers) != QueueHeldBehind || held(nextSteer, steers) != QueueHeldBehind {
		t.Fatalf("idle: queued=%q steer=%q next=%q, want nothing then behind", held(queued, steers), held(steer, steers), held(nextSteer, steers))
	}
	claimable(r.ClaimNextMessage, queued)
	if err := r.DeferMessage(queued, errors.New("target refused the message"), time.Now()); err != nil {
		t.Fatal(err)
	}
	if held(queued, steers) != QueueHeldRetrying || held(steer, steers) != QueueHeldBehind {
		t.Fatalf("deferred first message=%q second=%q, want retrying and behind", held(queued, steers), held(steer, steers))
	}
	if item, err := r.ClaimNextMessage("target", time.Now()); err != nil || item != nil {
		t.Fatalf("a message behind a deferred one was claimable: %+v err=%v", item, err)
	}
}

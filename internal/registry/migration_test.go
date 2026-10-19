package registry

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var migrationFixtureClock = time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC)

func openMigrationFixture(t *testing.T, name string, stampVersion int) *Registry {
	t.Helper()
	dump, err := os.ReadFile(filepath.Join("testdata", "migrations", name+".sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(string(dump)); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if stampVersion > 0 {
		if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, stampVersion)); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return r
}

func TestOpenMigratesHistoricalSchemas(t *testing.T) {
	for _, fixture := range []struct {
		name         string
		stampVersion int
		wantVersion  int
		source       string
		check        func(*testing.T, *Registry)
	}{
		{name: "v0", wantVersion: schemaVersion},
		{name: "v1", wantVersion: schemaVersion, check: assertLegacyStatusNotNormalized},
		{name: "v2", wantVersion: schemaVersion, source: "sender"},
		{name: "v3", wantVersion: schemaVersion, source: "sender"},
		{name: "v3", stampVersion: schemaVersion + 1, wantVersion: schemaVersion + 1, source: "sender"},
		{name: "v4", wantVersion: schemaVersion, source: "sender"},
		{name: "v5", wantVersion: schemaVersion, source: "sender"},
		{name: "v7", wantVersion: schemaVersion, source: "sender"},
		{name: "v8", wantVersion: schemaVersion, source: "sender", check: assertV8JournalAndProblemsMigrated},
		{name: "v9", wantVersion: schemaVersion, source: "sender"},
		{name: "v10", wantVersion: schemaVersion, source: "sender", check: assertV10SeedStatusMigrated},
		{name: "v11", wantVersion: schemaVersion, source: "sender", check: assertV11SeedCheckpointMigrated},
	} {
		label := fixture.name
		if fixture.stampVersion > 0 {
			label += "-stamped-newer"
		}
		t.Run(label, func(t *testing.T) {
			r := openMigrationFixture(t, fixture.name, fixture.stampVersion)
			var version int
			if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != fixture.wantVersion {
				t.Fatalf("user_version=%d err=%v, want %d", version, err, fixture.wantVersion)
			}
			assertMigratedSessionsAndRoutes(t, r)
			assertMigratedQueue(t, r, fixture.source)
			if fixture.check != nil {
				fixture.check(t, r)
			}
			assertMigratedRegistryAcceptsCurrentWrites(t, r)
			assertSchemaMatchesFreshRegistry(t, r)
			if identity, err := r.HostIdentity(); err != nil || !ValidHostID(identity.ID) || !ValidHostName(identity.Name) {
				t.Fatalf("host identity=%+v err=%v", identity, err)
			}
		})
	}
}

func assertSchemaMatchesFreshRegistry(t *testing.T, migrated *Registry) {
	t.Helper()
	fresh := openTestRegistry(t)
	want, got := schemaColumns(t, fresh), schemaColumns(t, migrated)
	for table, columns := range want {
		if got[table] == nil {
			t.Errorf("migrated registry is missing table %s", table)
			continue
		}
		for name, declaration := range columns {
			migrated := got[table][name]
			if strings.HasSuffix(declaration, " default=expression") {
				migrated = migrated[:max(strings.LastIndex(migrated, " default="), 0)] + " default=expression"
			}
			if migrated != declaration {
				t.Errorf("%s.%s migrated=%q fresh=%q", table, name, migrated, declaration)
			}
		}
		for name := range got[table] {
			if _, ok := columns[name]; !ok {
				t.Errorf("%s.%s exists only in the migrated registry", table, name)
			}
		}
	}
}

// schemaColumns reports each column's declaration. ALTER TABLE cannot add a
// column whose default is an expression, so upgraded rows carry a constant
// default there; every writer sets those columns explicitly.
func schemaColumns(t *testing.T, r *Registry) map[string]map[string]string {
	t.Helper()
	rows, err := r.db.Query(`SELECT m.name, p.name, p.type, p."notnull", COALESCE(p.dflt_value, '') FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := map[string]map[string]string{}
	for rows.Next() {
		var table, name, kind, fallback string
		var notNull int
		if err := rows.Scan(&table, &name, &kind, &notNull, &fallback); err != nil {
			t.Fatal(err)
		}
		if tables[table] == nil {
			tables[table] = map[string]string{}
		}
		if strings.HasPrefix(fallback, "datetime(") {
			fallback = "expression"
		}
		tables[table][name] = fmt.Sprintf("%s notnull=%d default=%s", kind, notNull, fallback)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func assertMigratedSessionsAndRoutes(t *testing.T, r *Registry) {
	t.Helper()
	target, err := r.Session("target")
	if err != nil || target.Name != "Target" || target.Cwd != "/work/target" || target.Surface != surface.KindCodex {
		t.Fatalf("target=%+v err=%v", target, err)
	}
	if _, err := r.Session("sender"); err != nil {
		t.Fatalf("sender: %v", err)
	}
	if owner, err := r.LookupAlias("builder"); err != nil || owner != "target" {
		t.Fatalf("alias owner=%q err=%v", owner, err)
	}
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if route := routes[0]; route.FromSession != "sender" || route.ToSession != "target" || !route.Active || route.Once {
		t.Fatalf("route=%+v", route)
	}
}

func assertMigratedQueue(t *testing.T, r *Registry, source string) {
	t.Helper()
	rows, err := r.ListQueue(true)
	if err != nil {
		t.Fatal(err)
	}
	delivered := false
	for _, row := range rows {
		if row.Message == "delivered hello" {
			delivered = row.Status == "delivered" && row.Evidence == surface.EvidenceDelivered
		}
	}
	if !delivered {
		t.Fatalf("delivered row was not carried forward: %+v", rows)
	}
	item, err := r.ClaimNextMessage("target", migrationFixtureClock)
	if err != nil || item == nil {
		t.Fatalf("pending claim=%+v err=%v", item, err)
	}
	if item.Message != "pending hello" || item.Operation != QueueOperationMessage || item.SourceSessionID != source || item.RelayHops != 0 || item.TurnOptions.Effort != "" || len(item.TurnOptions.OutputSchema) != 0 {
		t.Fatalf("pending item=%+v", item)
	}
	if err := r.AckMessage(item.ID); err != nil {
		t.Fatal(err)
	}
}

func assertMigratedRegistryAcceptsCurrentWrites(t *testing.T, r *Registry) {
	t.Helper()
	options := surface.TurnOptions{Effort: "xhigh", Mode: "plan", ServiceTier: "fast", OutputSchema: json.RawMessage(`{"type":"object"}`)}
	id, err := r.QueueMessageWithOptions("target", "after upgrade", "", surface.SendOptions{SourceSessionID: "sender", TurnOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := r.ListQueue(false)
	if err != nil {
		t.Fatal(err)
	}
	var listed *QueueRow
	for i := range rows {
		if rows[i].ID == id {
			listed = &rows[i]
		}
	}
	if listed == nil {
		t.Fatalf("queued row missing from %+v", rows)
	}
	row, err := r.QueueItem(id)
	if err != nil {
		t.Fatal(err)
	}
	claimed, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || claimed == nil || claimed.ID != id || claimed.SourceSessionID != "sender" {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
	want, _ := json.Marshal(options)
	for _, got := range []surface.TurnOptions{listed.TurnOptions, row.TurnOptions, claimed.TurnOptions} {
		if data, _ := json.Marshal(got); string(data) != string(want) {
			t.Fatalf("turn options=%s want=%s", data, want)
		}
	}
	if err := r.SaveRuntimeState("target", surface.TurnObservation{Status: surface.StatusIdle}); err != nil {
		t.Fatal(err)
	}
	state, found, err := r.RuntimeState("target")
	if err != nil || !found || state.RelayHops != 0 || state.NotificationArmed {
		t.Fatalf("runtime=%+v found=%v err=%v", state, found, err)
	}
	relayID, err := r.QueueRelayMessageWithOptions("target", "relay after upgrade", "relay:upgrade", 1, surface.SendOptions{SourceSessionID: "sender"})
	if err != nil {
		t.Fatal(err)
	}
	if expired, err := r.ExpireMessages(time.Now().Add(2 * queueMessageTTL)); err != nil || expired == 0 {
		t.Fatalf("expired=%d err=%v", expired, err)
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil {
		t.Fatal(err)
	}
	var relayed *DeliveryIntent
	for _, problem := range problems {
		intent, err := r.DeliveryIntent(problem.DeliveryID)
		if err != nil {
			t.Fatal(err)
		}
		if intent.QueueID == relayID {
			relayed = intent
		}
	}
	if relayed == nil || !relayed.Relayed || relayed.NotificationQueueID != 0 {
		t.Fatalf("relayed intent=%+v problems=%+v", relayed, problems)
	}
	assertMigratedRouteRelaysDurably(t, r)
}

func assertMigratedRouteRelaysDurably(t *testing.T, r *Registry) {
	t.Helper()
	routes, err := r.ListRoutes()
	if err != nil || len(routes) != 1 || routes[0].Handoff != HandoffTurnFinished || routes[0].EventCursor != 0 || routes[0].PendingCount != 0 || routes[0].LastError != "" {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	if err := r.CaptureRelayCompletions("sender", "", surface.CompletionBatch{Cursor: "after-upgrade", Events: []surface.Completion{{ID: "upgraded-turn", Text: "upgraded result"}}}); err != nil {
		t.Fatal(err)
	}
	routes, err = r.ListRoutes()
	if err != nil || routes[0].PendingCount != 1 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
	events, err := r.PendingRelayEvents(routes[0])
	if err != nil || len(events) != 1 {
		t.Fatalf("events=%+v err=%v", events, err)
	}
	queueID, err := r.ConsumeRelay(routes[0], events[0], "[agenthail relay hops=1] upgraded result", "queue", "")
	if err != nil || queueID == 0 {
		t.Fatalf("queueID=%d err=%v", queueID, err)
	}
	before := r.QueueCount("sender")
	if _, err := r.ExpireMessages(time.Now().Add(2 * queueMessageTTL)); err != nil {
		t.Fatal(err)
	}
	if after := r.QueueCount("sender"); after != before {
		t.Fatalf("relay source notices before=%d after=%d", before, after)
	}
	routes, err = r.ListRoutes()
	if err != nil || routes[0].EventCursor != events[0].ID || routes[0].FireCount != 1 || routes[0].PendingCount != 0 {
		t.Fatalf("routes=%+v err=%v", routes, err)
	}
}

func assertLegacyStatusNotNormalized(t *testing.T, r *Registry) {
	t.Helper()
	rows, err := r.ListQueue(true)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.Message == "legacy blank status" {
			if row.Status != "" || row.SourceSessionID != "" {
				t.Fatalf("legacy row was normalized during upgrade: %+v", row)
			}
			return
		}
	}
	t.Fatalf("legacy row missing from %+v", rows)
}

func assertV8JournalAndProblemsMigrated(t *testing.T, r *Registry) {
	t.Helper()
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "target", Kind: "item", ProviderKey: "next", Payload: []byte("q")}, SessionJournalRetention{Count: 4, Bytes: 10}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.SessionJournalBody("target", "body-ref", 0, 1); err == nil {
		t.Fatal("old body remained after retention counted its bytes")
	}
	problems, err := r.ListDeliveryProblems()
	if err != nil || len(problems) != 1 || problems[0].Reason != "transport failed" {
		t.Fatalf("problems=%+v err=%v", problems, err)
	}
	if changed, err := r.DismissDeliveryProblem(problems[0].DeliveryID); err != nil || !changed {
		t.Fatalf("dismiss changed=%v err=%v", changed, err)
	}
	if problems, err := r.ListDeliveryProblems(); err != nil || len(problems) != 0 {
		t.Fatalf("dismissed problems=%+v err=%v", problems, err)
	}
}

func assertV10SeedStatusMigrated(t *testing.T, r *Registry) {
	t.Helper()
	status, seq, identity, err := r.SessionJournalSeedCheckpoint("target")
	if err != nil || status != SessionJournalSeedUnknown || seq != 0 || identity != "" {
		t.Fatalf("checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
	if err := r.MarkSessionJournalSeed("target", true); err != nil {
		t.Fatal(err)
	}
	status, seq, identity, err = r.SessionJournalSeedCheckpoint("target")
	if err != nil || status != SessionJournalSeeded || seq != 1 || identity != "" {
		t.Fatalf("seeded checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
}

func assertV11SeedCheckpointMigrated(t *testing.T, r *Registry) {
	t.Helper()
	status, seq, identity, err := r.SessionJournalSeedCheckpoint("target")
	if err != nil || status != SessionJournalSeeded || seq != 1 || identity != "" {
		t.Fatalf("checkpoint status=%q seq=%d identity=%q err=%v", status, seq, identity, err)
	}
}

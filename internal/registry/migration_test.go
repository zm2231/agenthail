package registry

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var migrationFixtureClock = time.Date(2026, 9, 1, 0, 30, 0, 0, time.UTC)

func openMigrationFixture(t *testing.T, name string) *Registry {
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
		name        string
		wantVersion int
		source      string
		check       func(*testing.T, *Registry)
	}{
		{name: "v0", wantVersion: schemaVersion},
		{name: "v1", wantVersion: schemaVersion, check: assertLegacyStatusNotNormalized},
		{name: "v2", wantVersion: schemaVersion, source: "sender"},
		{name: "v3", wantVersion: schemaVersion, source: "sender"},
		{name: "v4", wantVersion: schemaVersion, source: "sender"},
		{name: "v5", wantVersion: schemaVersion, source: "sender"},
		{name: "v7", wantVersion: schemaVersion, source: "sender"},
		{name: "v8", wantVersion: schemaVersion, source: "sender", check: assertV8JournalAndProblemsMigrated},
		{name: "v9", wantVersion: schemaVersion, source: "sender"},
		{name: "v10", wantVersion: schemaVersion, source: "sender", check: assertV10SeedStatusMigrated},
		{name: "v11", wantVersion: schemaVersion, source: "sender", check: assertV11SeedCheckpointMigrated},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			r := openMigrationFixture(t, fixture.name)
			var version int
			if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != fixture.wantVersion {
				t.Fatalf("user_version=%d err=%v, want %d", version, err, fixture.wantVersion)
			}
			assertMigratedSessionsAndRoutes(t, r)
			if exists, err := queueHasExpiry(r); err != nil || exists {
				t.Fatalf("expires_at_ms exists=%v err=%v", exists, err)
			}
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
	assertSameColumns(t, schemaColumns(t, fresh.db), schemaColumns(t, migrated.db))
	want, got := schemaIndexes(t, fresh.db), schemaIndexes(t, migrated.db)
	for name, definition := range want {
		if got[name] != definition {
			t.Errorf("index %s migrated=%q fresh=%q", name, got[name], definition)
		}
	}
	for name := range got {
		if _, ok := want[name]; !ok {
			t.Errorf("index %s exists only in the migrated registry", name)
		}
	}
}

func TestSchemaDeclaresEveryColumn(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "schema.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	assertSameColumns(t, schemaColumns(t, openTestRegistry(t).db), schemaColumns(t, db))
}

func assertSameColumns(t *testing.T, want, got map[string]map[string]string) {
	t.Helper()
	for table, columns := range want {
		if got[table] == nil {
			t.Errorf("missing table %s", table)
			continue
		}
		for name, declaration := range columns {
			migrated := got[table][name]
			if strings.HasSuffix(declaration, " default=expression") {
				migrated = migrated[:max(strings.LastIndex(migrated, " default="), 0)] + " default=expression"
			}
			if migrated != declaration {
				t.Errorf("%s.%s got=%q want=%q", table, name, migrated, declaration)
			}
		}
		for name := range got[table] {
			if _, ok := columns[name]; !ok {
				t.Errorf("%s.%s is unexpected", table, name)
			}
		}
	}
	for table := range got {
		if want[table] == nil {
			t.Errorf("table %s is unexpected", table)
		}
	}
}

func schemaIndexes(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT name, sql FROM sqlite_master WHERE type='index' AND sql IS NOT NULL`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexes := map[string]string{}
	for rows.Next() {
		var name, definition string
		if err := rows.Scan(&name, &definition); err != nil {
			t.Fatal(err)
		}
		indexes[name] = strings.Join(strings.Fields(definition), " ")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return indexes
}

// schemaColumns reports each column's declaration. ALTER TABLE cannot add a
// column whose default is an expression, so upgraded rows carry a constant
// default there; every writer sets those columns explicitly.
func schemaColumns(t *testing.T, db *sql.DB) map[string]map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT m.name, p.name, p.type, p."notnull", COALESCE(p.dflt_value, '') FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type='table' AND m.name NOT LIKE 'sqlite_%'`)
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
	if err != nil || target.Name != "Target" || target.Cwd != "/work/target" || target.Surface != surface.KindCodex || target.RuntimeKind == "" {
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
	rows, err := r.ListQueue(true, nil)
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
	rows, err := r.ListQueue(false, nil)
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
	row, err := r.QueueItem(id, nil)
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
	failQueued(t, r, relayID)
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
	failQueued(t, r, queueID)
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
	rows, err := r.ListQueue(true, nil)
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

func TestOpenRetiresQueueExpiry(t *testing.T) {
	dump, err := os.ReadFile(filepath.Join("testdata", "migrations", "v11.sql"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "registry.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		string(dump),
		`INSERT INTO message_queue(id,session_id,message,queued_at,status,evidence,last_error,source_session_id,expires_at_ms,updated_at) VALUES(50,'target','old report','2026-10-18 08:08:00','expired','expired','message expired after 1 hour','sender',1,'2026-10-18 09:08:00')`,
		`INSERT INTO delivery_intents(id,sender_session_id,target_session_id,message,queue_id,status,evidence,failure) VALUES(50,'sender','target','old report',50,'expired','expired','message expired after 1 hour')`,
		`INSERT INTO message_queue(id,session_id,message,queued_at,status,last_error,expires_at_ms,updated_at) VALUES(51,'target','failed earlier','2026-10-18 07:00:00','dead','delivery failed',1,'2026-10-18 08:00:00')`,
		`INSERT INTO delivery_history(kind,session_id,queue_id,message,result) VALUES('expired','target',50,'old report','removed after 1 hour without delivery')`,
	} {
		if _, err := db.Exec(statement); err != nil {
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
	defer r.Close()
	if exists, err := queueHasExpiry(r); err != nil || exists {
		t.Fatalf("expires_at_ms exists=%v err=%v", exists, err)
	}
	if row, err := r.QueueItem(50, nil); err != nil || row.Status != "canceled" || row.Evidence != surface.EvidenceCanceled || row.LastError != "expired before delivery" || !row.Historical {
		t.Fatalf("expired row=%+v err=%v", row, err)
	}
	if row, err := r.QueueItem(2, nil); err != nil || row.Status != "pending" {
		t.Fatalf("pending row=%+v err=%v", row, err)
	}
	if row, err := r.QueueItem(51, nil); err != nil || row.DeadAt != time.Date(2026, 10, 18, 8, 0, 0, 0, time.UTC).UnixMilli() {
		t.Fatalf("dead row=%+v err=%v", row, err)
	}
	if intent, err := r.DeliveryIntent(50); err != nil || intent.Status != DeliveryIntentFailed || intent.Evidence != surface.EvidenceFailed || intent.Failure != "expired before delivery" {
		t.Fatalf("intent=%+v err=%v", intent, err)
	}
	history, err := r.ListHistory(50, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range history {
		if entry.Kind == "expired" {
			t.Fatalf("history kept expired entry %+v", entry)
		}
	}
}

func TestRuntimeKindStepDerivesEachRowsKindFromItsSurfaceAndTransport(t *testing.T) {
	r := openTestRegistry(t)
	if _, err := r.db.Exec(`ALTER TABLE sessions DROP COLUMN runtime_kind`); err != nil {
		t.Fatal(err)
	}
	want := map[string]surface.RuntimeKind{
		"claude":         surface.RuntimeClaudeCLI,
		"codex-managed":  surface.RuntimeCodexAppServer,
		"codex-desktop":  surface.RuntimeCodexDesktop,
		"codex-readonly": surface.RuntimeCodexDesktop,
		"codex-none":     surface.RuntimeCodexDesktop,
		"notion":         surface.RuntimeNotionWeb,
		"operator":       surface.RuntimeOperator,
		"unknown":        "",
	}
	for _, row := range []struct{ id, surface, transport string }{
		{"claude", "claude", "uds"},
		{"codex-managed", "codex", "managed"},
		{"codex-desktop", "codex", "desktop"},
		{"codex-readonly", "codex", "readOnly"},
		{"codex-none", "codex", ""},
		{"notion", "notion", ""},
		{"operator", "agenthail", ""},
		{"unknown", "retired", ""},
	} {
		if _, err := r.db.Exec(`INSERT INTO sessions(id,surface,name,transport) VALUES(?,?,?,?)`, row.id, row.surface, row.id, row.transport); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.migrate(true); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Exec(`INSERT INTO sessions(id,surface,name,transport,runtime_kind) VALUES('recorded','codex','recorded','managed','codex-desktop')`); err != nil {
		t.Fatal(err)
	}
	want["recorded"] = surface.RuntimeCodexDesktop
	if err := r.migrate(true); err != nil {
		t.Fatal(err)
	}
	for id, kind := range want {
		var got string
		if err := r.db.QueryRow(`SELECT runtime_kind FROM sessions WHERE id=?`, id).Scan(&got); err != nil || surface.RuntimeKind(got) != kind {
			t.Fatalf("%s runtime_kind=%q err=%v, want %q", id, got, err, kind)
		}
	}
}

func TestOpenRetiresQueueExpiryLeftOnARegistryAtTheVersionBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "target")
	for _, statement := range []string{
		`ALTER TABLE message_queue ADD COLUMN expires_at_ms INTEGER NOT NULL DEFAULT 0`,
		`INSERT INTO message_queue(id,session_id,message,queued_at,status,evidence,last_error,expires_at_ms,updated_at) VALUES(60,'target','old report','2026-10-18 08:08:00','expired','expired','message expired after 1 hour',1,'2026-10-18 09:08:00')`,
		`PRAGMA user_version=16`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	if exists, err := queueHasExpiry(r); err != nil || exists {
		t.Fatalf("expires_at_ms exists=%v err=%v", exists, err)
	}
	if row, err := r.QueueItem(60, nil); err != nil || row.Status != "canceled" || row.LastError != "expired before delivery" {
		t.Fatalf("expired row=%+v err=%v", row, err)
	}
}

func queueHasExpiry(r *Registry) (bool, error) {
	tx, err := r.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	return columnExists(tx, "message_queue", "expires_at_ms")
}

func TestOpenGivesDevicesOnARegistryAtTheVersionBeforeNoHostBinding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE paired_devices DROP COLUMN host_id`,
		`INSERT INTO paired_devices(id,name,token_hash,scopes,created_at) VALUES('device-1','vps','hash-1','control,host,read','2026-10-19T08:00:00Z')`,
		`PRAGMA user_version=20`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	devices, err := r.ListDevices(false)
	if err != nil || len(devices) != 1 || !devices[0].IsHost() || devices[0].HostID != "" {
		t.Fatalf("devices=%+v err=%v", devices, err)
	}
}

func TestOpenAddsQueueUploadsToARegistryAtTheVersionBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "target")
	for _, statement := range []string{
		`ALTER TABLE message_queue DROP COLUMN uploads`,
		`INSERT INTO message_queue(id,session_id,message,status,updated_at) VALUES(70,'target','read the report','pending','2026-10-19 09:00:00')`,
		`PRAGMA user_version=19`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	claimed, err := r.ClaimNextMessage("target", time.Now())
	if err != nil || claimed == nil || claimed.ID != 70 || claimed.Message != "read the report" || len(claimed.Uploads) != 0 {
		t.Fatalf("claimed=%+v err=%v", claimed, err)
	}
}

func TestOpenUpgradesTheProjectsTableReleasedBeforeAddedProjects(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`DROP TABLE projects`,
		`CREATE TABLE projects (id TEXT PRIMARY KEY, kind TEXT NOT NULL, root_path TEXT NOT NULL, display_name TEXT NOT NULL, registered INTEGER NOT NULL DEFAULT 0, archived_at_ms INTEGER NOT NULL DEFAULT 0)`,
		`INSERT INTO projects VALUES('project-aaa','folder','/work/notes','notes',1,0)`,
		`INSERT INTO projects VALUES('project-bbb','repo','/work/app','app',0,1760000000000)`,
		`INSERT INTO projects VALUES('project-ccc','repo','/work/lib','lib',0,0)`,
		`INSERT INTO projects VALUES('project-ddd','folder','/work/site','site',1,0)`,
		`INSERT INTO projects VALUES('project-eee','repo','/work/site','site',0,1761000000000)`,
		`PRAGMA user_version=22`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	projects, err := r.ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ProjectRecord{}
	for _, project := range projects {
		got[project.WorkspaceRoot] = project
	}
	if len(got) != 3 || len(projects) != 3 {
		t.Fatalf("projects=%+v", projects)
	}
	if site := got["/work/site"]; site.ID != "p-project-ddd" || !site.Added || site.ArchivedAt.UnixMilli() != 1761000000000 {
		t.Fatalf("folder sharing a root with an archived repository=%+v", site)
	}
	if notes := got["/work/notes"]; notes.ID != "p-project-aaa" || !notes.Added || notes.Title != "notes" || !notes.ArchivedAt.IsZero() {
		t.Fatalf("registered folder=%+v", notes)
	}
	if app := got["/work/app"]; app.ID != "implicit:/work/app" || app.Added || app.ArchivedAt.UnixMilli() != 1760000000000 {
		t.Fatalf("archived repository=%+v", app)
	}
	var leftover int
	if err := r.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='projects_released'`).Scan(&leftover); err != nil || leftover != 0 {
		t.Fatalf("leftover=%d err=%v", leftover, err)
	}
}

func TestOpenAddsCoordinatorsToARegistryAtTheVersionBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "first", "second")
	for _, statement := range []string{
		`DROP TABLE coordinator_replaced`,
		`DROP TABLE coordinator_history`,
		`DROP TABLE coordinators`,
		`PRAGMA user_version=23`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	spec := CoordinatorSpec{ProjectID: "repo:example/app", ProjectRoot: "/work/app", Name: "app-lead", Surface: "codex", Cwd: "/work/app"}
	if _, err := r.SetCoordinator(spec, CoordinatorChange{SessionID: "first", ChangedBy: "fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := r.HandOverCoordinator(spec, "first", "second"); err != nil {
		t.Fatal(err)
	}
	coordinators, err := r.LocalCoordinators()
	if err != nil || len(coordinators) != 1 || coordinators[0].SessionID != "second" {
		t.Fatalf("coordinators=%+v err=%v", coordinators, err)
	}
	replaced, ok, err := r.CoordinatorOfSession("first")
	if err != nil || !ok || replaced.Name != "app-lead" {
		t.Fatalf("replaced conversation's coordinator=%+v ok=%v err=%v", replaced, ok, err)
	}
	history, err := r.CoordinatorHistory(spec.ProjectID)
	if err != nil || len(history) != 1 || history[0].SessionID != "first" {
		t.Fatalf("history=%+v err=%v", history, err)
	}
}

func TestOpenRefusesARegistryANewerAgenthailMigrated(t *testing.T) {
	for _, journal := range []string{"WAL", "DELETE"} {
		t.Run(journal, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "registry.db")
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			register(t, r, "target")
			newer := schemaVersion + 3
			if _, err := r.db.Exec(fmt.Sprintf(`PRAGMA user_version=%d`, newer)); err != nil {
				t.Fatal(err)
			}
			if journal == "DELETE" {
				if _, err := r.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.Close(); err != nil {
				t.Fatal(err)
			}
			if journal == "DELETE" {
				plain, err := sql.Open("sqlite", path)
				if err != nil {
					t.Fatal(err)
				}
				var mode string
				if err := plain.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
					t.Fatalf("journal_mode=%q err=%v", mode, err)
				}
				plain.Close()
			}
			before := registryFiles(t, path)
			opened, err := Open(path)
			var refused *NewerSchemaError
			if !errors.As(err, &refused) || refused.Found != newer || refused.Supported != schemaVersion {
				if opened != nil {
					opened.Close()
				}
				t.Fatalf("Open err=%v, want a NewerSchemaError for schema %d", err, newer)
			}
			if want := fmt.Sprintf("registry is schema %d; this agenthail supports %d; install a newer agenthail", newer, schemaVersion); err.Error() != want {
				t.Fatalf("message=%q want %q", err.Error(), want)
			}
			if after := registryFiles(t, path); !reflect.DeepEqual(after, before) {
				t.Fatalf("refused open changed the registry files: before=%v after=%v", before, after)
			}
		})
	}
}

func TestOpenWaitingOnANewerAgenthailsMigrationRefusesItsSchema(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	var accepted string
	if err := r.db.QueryRow(`PRAGMA journal_mode`).Scan(&accepted); err != nil || accepted != "wal" {
		t.Fatalf("an accepted registry is in journal_mode %q (err %v), want wal", accepted, err)
	}
	if _, err := r.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	r.Close()
	newer, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	defer newer.Close()
	newer.SetMaxOpenConns(1)
	var mode string
	if err := newer.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal_mode=%q err=%v", mode, err)
	}
	ctx := context.Background()
	conn, err := newer.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	opened := make(chan error, 1)
	go func() {
		r, err := Open(path)
		if r != nil {
			r.Close()
		}
		opened <- err
	}()
	time.Sleep(200 * time.Millisecond)
	stamp := schemaVersion + 1
	if _, err := conn.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version=%d`, stamp)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}
	var refused *NewerSchemaError
	if err := <-opened; !errors.As(err, &refused) || refused.Found != stamp {
		t.Fatalf("Open err=%v, want a NewerSchemaError for schema %d", err, stamp)
	}
	if err := conn.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("refused open changed journal_mode to %q (err %v)", mode, err)
	}
}

// registryFiles is the content of the database and its journals, keyed by name.
func registryFiles(t *testing.T, path string) map[string]string {
	t.Helper()
	files := map[string]string{}
	for _, name := range []string{path, path + "-wal", path + "-journal"} {
		data, err := os.ReadFile(name)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(data) > 0 {
			files[filepath.Base(name)] = fmt.Sprintf("%x", sha256.Sum256(data))
		}
	}
	return files
}

func TestOpenHoldsNoNameForALaunchPendingAtTheVersionBefore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "first")
	for _, statement := range []string{
		`DROP TABLE alias_reservations`,
		`DROP TABLE launcher_pending`,
		`CREATE TABLE launcher_pending (id INTEGER PRIMARY KEY AUTOINCREMENT, launcher TEXT NOT NULL, agent TEXT NOT NULL, cwd TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '', alias TEXT NOT NULL DEFAULT '', location BLOB NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))`,
		`INSERT INTO launcher_pending(launcher,agent,cwd,alias,location,created_at) VALUES('tmux','claude','/work/app','builder','{"workspace":"w"}','2026-10-18 07:35:43')`,
		`PRAGMA user_version=24`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	pending, err := r.PendingLaunches()
	if err != nil || len(pending) != 1 || pending[0].Alias.Name != "builder" || !pending[0].ExpiresAt.Before(time.Now()) {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	if err := r.SetAlias("builder", "first"); err != nil {
		t.Fatalf("a launch pending before the upgrade holds its name: %v", err)
	}
}

func TestOpenAddsTheTranscriptCheckpointToARegistryAtVersion31(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "session")
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "item-1", Payload: []byte("one")}, SessionJournalRetention{Count: 10, Bytes: 1024}); err != nil {
		t.Fatal(err)
	}
	for _, statement := range []string{
		`ALTER TABLE session_journal_state DROP COLUMN transcript_checkpoint`,
		`PRAGMA user_version=31`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	if checkpoint, found, err := r.SessionTranscriptCheckpoint("session"); err != nil || found {
		t.Fatalf("an upgraded journal has checkpoint %+v found=%v err=%v, want none", checkpoint, found, err)
	}
	want := TranscriptCheckpoint{Offset: 4096, Identity: "1:2"}
	if err := r.SetSessionTranscriptCheckpoint("session", want); err != nil {
		t.Fatal(err)
	}
	if got, found, err := r.SessionTranscriptCheckpoint("session"); err != nil || !found || got != want {
		t.Fatalf("checkpoint=%+v found=%v err=%v, want %+v", got, found, err, want)
	}
	window, err := r.SessionJournalAfter("session", 0, 10)
	if err != nil || len(window.Entries) != 1 {
		t.Fatalf("journal after upgrade = %+v err=%v", window, err)
	}
	if err := r.ClearSessionTranscriptCheckpoint("session"); err != nil {
		t.Fatal(err)
	}
	if _, found, err := r.SessionTranscriptCheckpoint("session"); err != nil || found {
		t.Fatalf("cleared checkpoint found=%v err=%v", found, err)
	}
}

func TestOpenPlacesTheRowsOfARegistryAtVersion32WhereTheirSeqWas(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	register(t, r, "session")
	retention := SessionJournalRetention{Count: 10, Bytes: 1024}
	for _, key := range []string{"one", "two", "three"} {
		if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: key, Payload: []byte(`"` + key + `"`)}, retention); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "one", Payload: []byte(`"one again"`)}, retention); err != nil {
		t.Fatal(err)
	}
	// Pages before the upgrade ran in seq order, so the newest row was the
	// upserted "one" at seq 4 and the next cursor was 4.
	const oldCursor = 4
	for _, statement := range []string{
		`DROP INDEX session_journal_position`,
		`ALTER TABLE session_journal DROP COLUMN position`,
		`ALTER TABLE session_journal_state DROP COLUMN pruned_seq`,
		`PRAGMA user_version=32`,
	} {
		if _, err := r.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	r, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	page, err := r.ReadSessionJournalPage("session", 0, 10)
	if err != nil || journalKeys(t, page.Entries) != "two three one" {
		t.Fatalf("upgraded page %q err=%v, want rows where their seq was", journalKeys(t, page.Entries), err)
	}
	for _, entry := range page.Entries {
		if entry.Position != entry.Seq {
			t.Fatalf("upgraded %s at %d with seq %d", entry.ProviderKey, entry.Position, entry.Seq)
		}
	}
	older, err := r.ReadSessionJournalPage("session", oldCursor, 10)
	if err != nil || journalKeys(t, older.Entries) != "two three" {
		t.Fatalf("a cursor from before the upgrade pages %q err=%v", journalKeys(t, older.Entries), err)
	}
	if _, _, err := r.AppendSessionJournalEntry(SessionJournalEntry{SessionID: "session", Kind: "item", ProviderKey: "two", Payload: []byte(`"two again"`)}, retention); err != nil {
		t.Fatal(err)
	}
	if page, err := r.ReadSessionJournalPage("session", 0, 10); err != nil || journalKeys(t, page.Entries) != "two three one" {
		t.Fatalf("after an upsert %q err=%v", journalKeys(t, page.Entries), err)
	}
}

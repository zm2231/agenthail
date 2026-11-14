package daemon

import (
	"database/sql"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
)

const (
	journalTables = "session transcripts, read only by session streams"
	deviceTables  = "paired devices, push and sign-in, read by the device and push paths"
	routingTables = "message routing and relays, which reach a view only through the queue and delivery intents"
	ledgerTables  = "project ledgers and project identity history, applied to catalog_sessions.host_project at discovery"
	sessionOnly   = "per-session state read by its own command or the launcher, not by any view query"
	queueMarks    = "mirrored sessions whose queue changed, read only by RefreshHostQueueCounts, which writes the count a view reads into catalog_sessions"
	idleAttempts  = "idle compaction attempts, read by the idle compactor and when a turn record is journaled, not by any view query"
	codexConfig   = "a Codex thread's config overrides, read when Agenthail resumes the thread and by session metadata, not by any view query"
)

var notSessionViewInputs = map[string]string{
	"session_journal": journalTables, "session_journal_bodies": journalTables, "session_journal_body_tail": journalTables, "session_journal_state": journalTables,
	"browser_push_subscriptions": deviceTables, "device_connection_drops": deviceTables, "device_streams": deviceTables, "device_pairings": deviceTables, "device_push_targets": deviceTables,
	"paired_devices": deviceTables, "push_outcomes": deviceTables, "push_sends": deviceTables, "signin_bindings": deviceTables, "signin_off": deviceTables,
	"signin_profiles": deviceTables, "session_credentials": deviceTables,
	"channel_members": routingTables, "channels": routingTables, "remote_channels": routingTables, "routes": routingTables, "relay_deliveries": routingTables, "relay_events": routingTables,
	"relay_provenance": routingTables, "relay_sources": routingTables, "action_receipts": routingTables,
	"ledger_backlog_told": ledgerTables, "ledger_events": ledgerTables, "ledger_grants": ledgerTables, "ledger_state": ledgerTables, "project_links": ledgerTables, "project_move_refusals": ledgerTables,
	"project_moves": ledgerTables, "removed_project_ledgers": ledgerTables, "repository_keys": ledgerTables, "project_moves_waiting": ledgerTables,
	"move_dropped_relays": "relays a move did not take over, read by doctor and the move's answer; the session view reads the delivery_history entry written with them",
	"alias_reservations":  sessionOnly, "launcher_pending": sessionOnly, "pending_ceilings": sessionOnly, "session_ceilings": sessionOnly,
	"session_context": sessionOnly, "session_rekeys": sessionOnly,
	"host_queue_dirty": queueMarks, "idle_compactions": idleAttempts,
	"session_codex_config": codexConfig, "trigger_runs": "only GET /api/v1/triggers reads trigger runs; a finished run reaches a session view as its delivery_history entry",
	"peer_relay_sockets": "only a Claude host's hold report reads which session a reply relay socket belongs to; the hold reaches a session view as its delivery intent",
	"project_settings":   "a project's settings reach the dashboard's project rows and the MCP socket, which reads them at each request; no session view shows them",
	"host_held_actions":  "a held channel leg belongs to a paired host, not a session; it reaches a session view only as the host's deliveries",
}

// Dropping session_archive or projects from the registry's presentation inputs
// leaves this test passing, and that is not a gap: the
// catalog_retired_version_* triggers on both tables (catalog_retired.go) bump
// catalog_retired_version, itself an input, on every change a view reads. The
// inputs stay as a second guard should those triggers change.
func TestEveryRegistryValueThatChangesASessionViewMovesItsCacheKey(t *testing.T) {
	f := newViewFixture(t)
	f.populate(rand.New(rand.NewSource(20261107)), 300, func(int, string) {})
	f.fill()
	db := openSweepDB(t, f.reg.Path())
	tables := queryStrings(t, db, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	for _, table := range tables {
		if _, listed := notSessionViewInputs[table]; !listed && len(queryStrings(t, db, fmt.Sprintf(`SELECT 1 FROM %q LIMIT 1`, table))) == 0 {
			t.Errorf("%s has no rows in the view fixture: add a write that fills it, or list it in notSessionViewInputs with why no session view reads it", table)
		}
	}
	// Each shard sweeps its share of the rows on its own copy of the one
	// fixture, since the fixture's ids and times differ from run to run.
	var busy, logged, moved int
	if err := db.QueryRow(`PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &logged, &moved); err != nil || busy != 0 {
		t.Fatalf("checkpoint the fixture's registry before copying it: busy=%d err=%v", busy, err)
	}
	stored, err := os.ReadFile(f.reg.Path())
	if err != nil {
		t.Fatal(err)
	}
	baseline := sessionViewStoredRender(t, f)
	for shard := range sessionViewSweepShards {
		path := filepath.Join(t.TempDir(), "registry.db")
		if err := os.WriteFile(path, stored, 0o600); err != nil {
			t.Fatal(err)
		}
		t.Run(fmt.Sprint("shard ", shard), func(t *testing.T) {
			t.Parallel()
			reg, err := registry.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { reg.Close() })
			copied := &viewFixture{d: newTestDaemon(t, reg, f.d.Surfaces), reg: reg, ctx: f.ctx, clock: f.clock}
			copied.d.presentation.clock = func() time.Time { return copied.clock }
			copied.d.presentation.epoch = f.d.presentation.epoch
			copied.d.setHostIdentity(f.d.hostIdentity())
			if sessionViewStoredRender(t, copied) != baseline {
				t.Fatal("a copy of the fixture's registry renders different session views than the fixture")
			}
			sweepSessionViewShard(t, copied, shard, tables)
		})
	}
}

// sessionViewSweepShards is how many copies of the fixture the sweep runs on
// at once; each takes every shard-th row.
const sessionViewSweepShards = 4

var sessionViewSweepRequests = []sessionViewRequest{{Scope: "all", Limit: 200, Members: true}, {Scope: "current", Limit: 200, Members: true}, {Scope: "archived", Limit: 200, Members: true}}

func sessionViewSweepRender(t *testing.T, f *viewFixture, stored *storedView) string {
	var out strings.Builder
	for _, request := range sessionViewSweepRequests {
		encoded, _ := f.read(t, request, stored)
		out.WriteString(encoded)
	}
	return out.String()
}

func sessionViewStoredRender(t *testing.T, f *viewFixture) string {
	stored := f.stored(t)
	defer stored.Close()
	return sessionViewSweepRender(t, f, stored)
}

func openSweepDB(t *testing.T, path string) *sql.DB {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	db.SetMaxOpenConns(1)
	return db
}

func sweepSessionViewShard(t *testing.T, f *viewFixture, shard int, tables []string) {
	db := openSweepDB(t, f.reg.Path())
	// This connection leaves foreign keys off, so deleting a parent here keeps
	// children the registry's cascade would remove; their own deletes are swept.
	cascades := map[string]bool{}
	for _, parent := range queryStrings(t, db, `SELECT DISTINCT f."table" FROM sqlite_schema m, pragma_foreign_key_list(m.name) f WHERE m.type='table' AND f.on_delete='CASCADE'`) {
		cascades[parent] = true
	}
	read := func(stored *storedView) string { return sessionViewSweepRender(t, f, stored) }
	render := func() string { return sessionViewStoredRender(t, f) }
	served := func(change string, stored string) {
		if read(nil) != stored {
			t.Errorf("after %s, a session view served through the caches differs from one built from the stored data", change)
		}
	}
	key := func() string {
		key, err := f.reg.ReadCatalogPresentationKey(f.ctx)
		return fmt.Sprint(key, err)
	}
	checked, unit := 0, -1
	// A restore appends to catalog_events like any write, so the log position
	// and the revision that hashes it move on, and so do the counters that
	// version the stored data. Anything else a trigger moved, such as a row a
	// changed parent unretired, is written back as the sweep began with it, so
	// every step starts from the same stored data and the same view.
	logPosition := regexp.MustCompile(`"revision":"[0-9a-f]+"|"catalogSeq":[0-9]+`)
	masked := func(view string) string { return logPosition.ReplaceAllString(view, "") }
	columnsOf, quotedRow := map[string]string{}, map[string]string{}
	state := func() map[string][]string {
		out := map[string][]string{}
		for _, table := range tables {
			if movesOnEveryWrite[table] {
				continue
			}
			if columnsOf[table] == "" {
				names := queryStrings(t, db, fmt.Sprintf(`SELECT '"'||replace(name,'"','""')||'"' FROM pragma_table_xinfo(%s) WHERE hidden=0`, sqlQuote(table)))
				columnsOf[table] = strings.Join(names, ",")
				quotedRow[table] = "quote(" + strings.Join(names, ")||','||quote(") + ")"
			}
			rows := queryStrings(t, db, fmt.Sprintf(`SELECT %s FROM %q`, quotedRow[table], table))
			slices.Sort(rows)
			out[table] = rows
		}
		return out
	}
	baseline, settled, restored := masked(render()), state(), "filling the fixture"
	unchanged := func() {
		for table, rows := range state() {
			if slices.Equal(rows, settled[table]) {
				continue
			}
			mustExec(t, db, fmt.Sprintf(`DELETE FROM %q`, table))
			for _, row := range settled[table] {
				mustExec(t, db, fmt.Sprintf(`INSERT INTO %q(%s) VALUES(%s)`, table, columnsOf[table], row))
			}
		}
		for table, rows := range state() {
			if !slices.Equal(rows, settled[table]) {
				t.Fatalf("after %s, %s could not be written back as the sweep began with it", restored, table)
			}
		}
		if masked(read(nil)) != baseline {
			t.Errorf("after %s, a session view served through the caches differs from one built from the stored data", restored)
		}
	}
	for _, table := range tables {
		rowKey, keepKey, restoreColumns, restoreValues := sessionViewRowKey(t, db, table)
		where := ` WHERE ` + rowKey + `=?`
		if len(queryStrings(t, db, fmt.Sprintf(`SELECT 1 FROM %q LIMIT 1`, table))) == 0 {
			continue
		}
		// A key is swept where a write can rewrite it: not the rowid that
		// names the row here, and not one other rows reference, which the
		// registry's foreign keys hold in place.
		columns := queryStrings(t, db, fmt.Sprintf(`SELECT name FROM pragma_table_xinfo(%[1]s) WHERE hidden=0 AND (pk=0 OR (%[2]s
			AND NOT (upper(type)='INTEGER' AND (SELECT COUNT(*) FROM pragma_table_xinfo(%[1]s) WHERE pk>0)=1)
			AND NOT EXISTS (SELECT 1 FROM sqlite_schema m, pragma_foreign_key_list(m.name) f WHERE m.type='table' AND f."table"=%[1]s)))`, sqlQuote(table), fmt.Sprint(rowKey == "rowid")))
		stored := queryStrings(t, db, fmt.Sprintf(`SELECT '"'||replace(name,'"','""')||'"' FROM pragma_table_xinfo(%s) WHERE hidden=0`, sqlQuote(table)))
		identical := make([]string, 0, len(columns))
		values := make([]string, 0, len(columns))
		for _, column := range columns {
			identical = append(identical, fmt.Sprintf("%[1]q=%[1]q", column))
			values = append(values, fmt.Sprintf("%q", column))
		}
		quoted := strings.Join(values, ",")
		keys, perturbed := sweepRows(t, db, table, rowKey, columns, strings.Join(stored, ","))
		for _, rowKeyValue := range keys {
			unit++
			if unit%sessionViewSweepShards != shard {
				continue
			}
			if len(identical) > 0 {
				from := key()
				mustExec(t, db, fmt.Sprintf(`UPDATE %q SET %s`, table, strings.Join(identical, ","))+where, rowKeyValue)
				if key() != from {
					t.Errorf("rewriting a %s row with the values it already has moves the cache key: its trigger must watch value changes, not writes", table)
				}
			}
			list := strings.Join(stored, ",")
			mustExec(t, db, fmt.Sprintf(`CREATE TEMP TABLE kept AS SELECT %s%s FROM %q`, keepKey, list, table)+where, rowKeyValue)
			from := key()
			unchanged()
			mustExec(t, db, fmt.Sprintf(`DELETE FROM %q`, table)+where, rowKeyValue)
			after, to := render(), key()
			if !versionCounters[table] && !cascades[table] {
				served("deleting a "+table+" row", after)
			}
			mustExec(t, db, fmt.Sprintf(`INSERT INTO %q(%s%s) SELECT %s%s FROM temp.kept`, table, restoreColumns, list, restoreValues, list))
			mustExec(t, db, `DROP TABLE temp.kept`)
			restored = "deleting and restoring a " + table + " row"
			checked++
			if masked(after) != baseline && to == from {
				t.Errorf("deleting a %s row changes a session view but not its cache key", table)
			}
			for _, column := range perturbed[rowKeyValue] {
				mustExec(t, db, fmt.Sprintf(`CREATE TEMP TABLE kept AS SELECT %s FROM %q`, quoted, table)+where, rowKeyValue)
				from := key()
				unchanged()
				if _, err := db.Exec(fmt.Sprintf(`UPDATE %q SET %[2]q=`+perturbedValue, table, column)+movedWith[table+"."+column]+where, rowKeyValue); err != nil {
					mustExec(t, db, `DROP TABLE temp.kept`)
					continue
				}
				after, to := render(), key()
				served("changing "+table+"."+column, after)
				mustExec(t, db, fmt.Sprintf(`UPDATE %q SET (%s)=(SELECT %[2]s FROM temp.kept)`, table, quoted)+where, rowKeyValue)
				restored = "changing and restoring " + table + "." + column
				mustExec(t, db, `DROP TABLE temp.kept`)
				checked++
				if masked(after) != baseline && to == from {
					t.Errorf("changing %s.%s changes a session view but not its cache key", table, column)
				}
			}
		}
	}
	t.Logf("checked %d values", checked)
}

// sweepValues is how many distinct values a column may have for the sweep to
// take one row holding each; a column with more is changed at its least and
// greatest value and at an empty one.
const sweepValues = 4

// sweepRows picks, for each column, the rows to change it on. A row holding one
// of a few-valued column's values has every column changed, since what such a
// row holds decides what else shows, as a parent does for a subagent's
// nickname. Among rows holding the same value it prefers by shape, the
// few-valued columns' values and which of the others are empty, so the pick
// does not depend on the order the fixture's writes inserted rows in.
func sweepRows(t *testing.T, db *sql.DB, table, rowKey string, columns []string, content string) ([]string, map[string][]string) {
	t.Helper()
	few := map[string]bool{}
	var shape []string
	for _, column := range columns {
		var distinct int
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(DISTINCT %q) FROM %q`, column, table)).Scan(&distinct); err != nil {
			t.Fatal(err)
		}
		few[column] = distinct <= sweepValues
		if few[column] {
			shape = append(shape, fmt.Sprintf(`%q`, column))
		} else {
			shape = append(shape, fmt.Sprintf(`(%[1]q IS NULL OR %[1]q IN ('',0,x''))`, column))
		}
	}
	order := strings.Join(append(shape, content), ",")
	perturbed := map[string][]string{}
	change := func(query string, changed []string) {
		for _, key := range queryStrings(t, db, query) {
			for _, column := range changed {
				if !slices.Contains(perturbed[key], column) {
					perturbed[key] = append(perturbed[key], column)
				}
			}
		}
	}
	for _, column := range columns {
		if few[column] {
			change(fmt.Sprintf(`SELECT k FROM (SELECT %[3]s AS k,ROW_NUMBER() OVER (PARTITION BY %[2]q ORDER BY %[4]s) AS n FROM %[1]q) WHERE n=1`, table, column, rowKey, order), columns)
			continue
		}
		for _, end := range []string{fmt.Sprintf(`%q,`, column), fmt.Sprintf(`%q DESC,`, column)} {
			change(fmt.Sprintf(`SELECT %s FROM %q ORDER BY %s%s LIMIT 1`, rowKey, table, end, order), []string{column})
		}
		change(fmt.Sprintf(`SELECT %[3]s FROM %[1]q WHERE %[2]q IS NULL OR %[2]q IN ('',0,x'') ORDER BY %[4]s LIMIT 1`, table, column, rowKey, order), []string{column})
	}
	keys := queryStrings(t, db, fmt.Sprintf(`SELECT %s FROM %q ORDER BY %s`, rowKey, table, order))
	first := keys[0]
	return slices.DeleteFunc(keys, func(key string) bool { _, picked := perturbed[key]; return !picked && key != first }), perturbed
}

// movedWith names the columns every writer changes together with a column, so
// the sweep does not build a state no write produces.
var movedWith = map[string]string{
	"catalog_sessions.projection_fingerprint": ",projection_generation=projection_generation+1",
}

// movesOnEveryWrite are the catalog's event log and the counters that version
// the stored data, which a restore moves on like any other write.
var movesOnEveryWrite = map[string]bool{"catalog_events": true, "catalog_presentation_version": true, "catalog_retired_version": true}

// versionCounters are single-row counters that are only ever incremented; a
// cache keyed on one cannot be served once its row is gone, and no write
// deletes it.
var versionCounters = map[string]bool{"catalog_retired_version": true}

const perturbedValue = `CASE typeof(%[2]q) WHEN 'integer' THEN %[2]q=0 WHEN 'real' THEN %[2]q+1
 WHEN 'text' THEN CASE WHEN %[2]q='' THEN 'x' ELSE '' END WHEN 'blob' THEN CASE WHEN length(%[2]q)=0 THEN x'78' ELSE x'' END ELSE 1 END`

// sessionViewRowKey names a row by its rowid, or by its primary key in a table
// without one; a delete and reinsert keep either as it was.
func sessionViewRowKey(t *testing.T, db *sql.DB, table string) (rowKey, keep, restoreColumns, restoreValues string) {
	t.Helper()
	withoutRowid := queryStrings(t, db, fmt.Sprintf(`SELECT wr FROM pragma_table_list WHERE schema='main' AND name=%s`, sqlQuote(table)))
	if len(withoutRowid) == 1 && withoutRowid[0] == "1" {
		key := queryStrings(t, db, fmt.Sprintf(`SELECT '"'||replace(name,'"','""')||'"' FROM pragma_table_xinfo(%s) WHERE pk>0 ORDER BY pk`, sqlQuote(table)))
		return "json_array(" + strings.Join(key, ",") + ")", "", "", ""
	}
	return "rowid", "rowid AS kept_rowid,", "rowid,", "kept_rowid,"
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func queryStrings(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		out = append(out, value)
	}
	return out
}

func sqlQuote(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }

package daemon

import (
	"database/sql"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

const (
	journalTables  = "session transcripts, read only by session streams"
	deviceTables   = "paired devices, push and sign-in, read by the device and push paths"
	routingTables  = "message routing and relays, which reach a view only through the queue and delivery intents"
	ledgerTables   = "project ledgers and project identity history, applied to catalog_sessions.host_project at discovery"
	sessionOnly    = "per-session state read by its own command or the launcher, not by any view query"
	voiceOperators = "voice operator roles, read by LocalSessionRoles, which the view build does not call"
)

var notSessionViewInputs = map[string]string{
	"session_journal": journalTables, "session_journal_bodies": journalTables, "session_journal_body_tail": journalTables, "session_journal_state": journalTables,
	"browser_push_subscriptions": deviceTables, "device_connection_drops": deviceTables, "device_pairings": deviceTables, "device_push_targets": deviceTables,
	"paired_devices": deviceTables, "push_outcomes": deviceTables, "push_sends": deviceTables, "signin_bindings": deviceTables, "signin_off": deviceTables,
	"signin_profiles": deviceTables, "session_credentials": deviceTables,
	"channel_members": routingTables, "channels": routingTables, "routes": routingTables, "relay_deliveries": routingTables, "relay_events": routingTables,
	"relay_provenance": routingTables, "relay_sources": routingTables, "action_receipts": routingTables,
	"ledger_events": ledgerTables, "ledger_grants": ledgerTables, "ledger_state": ledgerTables, "project_links": ledgerTables, "project_move_refusals": ledgerTables,
	"project_moves": ledgerTables, "removed_project_ledgers": ledgerTables, "repository_keys": ledgerTables,
	"alias_reservations": sessionOnly, "launcher_pending": sessionOnly, "pending_ceilings": sessionOnly, "session_ceilings": sessionOnly,
	"session_context": sessionOnly, "session_moves": sessionOnly, "session_rekeys": sessionOnly, "tasks": sessionOnly,
	"session_origins": voiceOperators,
	"work_records":    "task worktrees, copied into a session's catalog projection when it is published; views read that copy",
}

func TestEveryRegistryValueThatChangesASessionViewMovesItsCacheKey(t *testing.T) {
	f := newViewFixture(t)
	db, err := sql.Open("sqlite", f.reg.Path()+"?_pragma=busy_timeout(15000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tables := queryStrings(t, db, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	f.populate(rand.New(rand.NewSource(20261107)), 300, func(int, string) {})
	f.fill()
	requests := []sessionViewRequest{{Scope: "all", Limit: 200, Members: true}, {Scope: "current", Limit: 200, Members: true}, {Scope: "archived", Limit: 200, Members: true}}
	render := func() string {
		var out strings.Builder
		for _, request := range requests {
			encoded, _ := f.read(t, request, true)
			out.WriteString(encoded)
		}
		return out.String()
	}
	key := func() string {
		key, err := f.reg.ReadCatalogPresentationKey(f.ctx)
		return fmt.Sprint(key, err)
	}
	checked := 0
	for _, table := range tables {
		rowids := queryStrings(t, db, fmt.Sprintf(`SELECT rowid FROM %[1]q WHERE rowid IN (SELECT rowid FROM %[1]q ORDER BY rowid LIMIT 5) OR rowid IN (SELECT rowid FROM %[1]q ORDER BY rowid DESC LIMIT 5) ORDER BY rowid`, table))
		if len(rowids) == 0 {
			if _, listed := notSessionViewInputs[table]; !listed {
				t.Errorf("%s has no rows in the view fixture: add a write that fills it, or list it in notSessionViewInputs with why no session view reads it", table)
			}
			continue
		}
		columns := queryStrings(t, db, fmt.Sprintf(`SELECT name FROM pragma_table_xinfo(%s) WHERE pk=0 AND hidden=0`, sqlQuote(table)))
		stored := queryStrings(t, db, fmt.Sprintf(`SELECT '"'||replace(name,'"','""')||'"' FROM pragma_table_xinfo(%s) WHERE hidden=0`, sqlQuote(table)))
		for _, rowid := range rowids {
			list := strings.Join(stored, ",")
			mustExec(t, db, fmt.Sprintf(`CREATE TEMP TABLE kept AS SELECT rowid AS kept_rowid,%s FROM %q WHERE rowid=?`, list, table), rowid)
			before, from := render(), key()
			mustExec(t, db, fmt.Sprintf(`DELETE FROM %q WHERE rowid=?`, table), rowid)
			after, to := render(), key()
			mustExec(t, db, fmt.Sprintf(`INSERT INTO %q(rowid,%s) SELECT kept_rowid,%s FROM temp.kept`, table, list, list))
			mustExec(t, db, `DROP TABLE temp.kept`)
			checked++
			if after != before && to == from {
				t.Errorf("deleting a %s row changes a session view but not its cache key", table)
			}
			for _, column := range columns {
				mustExec(t, db, fmt.Sprintf(`CREATE TEMP TABLE kept AS SELECT %q AS value FROM %q WHERE rowid=?`, column, table), rowid)
				before, from := render(), key()
				if _, err := db.Exec(fmt.Sprintf(`UPDATE %q SET %[2]q=`+perturbedValue+` WHERE rowid=?`, table, column), rowid); err != nil {
					mustExec(t, db, `DROP TABLE temp.kept`)
					continue
				}
				after, to := render(), key()
				mustExec(t, db, fmt.Sprintf(`UPDATE %q SET %q=(SELECT value FROM temp.kept) WHERE rowid=?`, table, column), rowid)
				mustExec(t, db, `DROP TABLE temp.kept`)
				checked++
				if after != before && to == from {
					t.Errorf("changing %s.%s changes a session view but not its cache key", table, column)
				}
			}
		}
	}
	t.Logf("checked %d values", checked)
}

const perturbedValue =`CASE typeof(%[2]q) WHEN 'integer' THEN %[2]q=0 WHEN 'real' THEN %[2]q+1
 WHEN 'text' THEN CASE WHEN %[2]q='' THEN 'x' ELSE '' END WHEN 'blob' THEN CASE WHEN length(%[2]q)=0 THEN x'78' ELSE x'' END ELSE 1 END`

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

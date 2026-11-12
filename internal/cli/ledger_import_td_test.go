package cli

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/ledger"
)

const tdFixtureSchema = `
CREATE TABLE issues (id TEXT PRIMARY KEY, title TEXT NOT NULL, description TEXT DEFAULT '', status TEXT NOT NULL DEFAULT 'open',
	type TEXT NOT NULL DEFAULT 'task', priority TEXT NOT NULL DEFAULT 'P2', points INTEGER DEFAULT 0, labels TEXT DEFAULT '',
	parent_id TEXT DEFAULT '', acceptance TEXT DEFAULT '', created_at DATETIME NOT NULL, deleted_at DATETIME, minor INTEGER DEFAULT 0,
	creator_session TEXT DEFAULT '', sprint TEXT DEFAULT '', defer_until TEXT, due_date TEXT);
CREATE TABLE comments (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, session_id TEXT NOT NULL, text TEXT NOT NULL, created_at DATETIME NOT NULL);
CREATE TABLE logs (id TEXT PRIMARY KEY, issue_id TEXT DEFAULT '', session_id TEXT NOT NULL, message TEXT NOT NULL, type TEXT NOT NULL DEFAULT 'progress', timestamp DATETIME NOT NULL);
CREATE TABLE handoffs (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, session_id TEXT NOT NULL, done TEXT DEFAULT '[]', remaining TEXT DEFAULT '[]',
	decisions TEXT DEFAULT '[]', uncertain TEXT DEFAULT '[]', timestamp DATETIME NOT NULL);
CREATE TABLE issue_reviews (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, reviewer_session TEXT NOT NULL, decision TEXT NOT NULL, summary TEXT NOT NULL DEFAULT '',
	created_at DATETIME NOT NULL, superseded_at DATETIME, reviewed_by TEXT NOT NULL DEFAULT '');
CREATE TABLE issue_dependencies (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, depends_on_id TEXT NOT NULL, relation_type TEXT NOT NULL DEFAULT 'depends_on');
CREATE TABLE notes (id TEXT PRIMARY KEY, title TEXT NOT NULL, content TEXT NOT NULL);
CREATE TABLE issue_files (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL, file_path TEXT NOT NULL);
CREATE TABLE git_snapshots (id TEXT PRIMARY KEY, issue_id TEXT NOT NULL);
CREATE TABLE work_sessions (id TEXT PRIMARY KEY, name TEXT NOT NULL);
CREATE TABLE boards (id TEXT PRIMARY KEY, name TEXT NOT NULL, is_builtin INTEGER NOT NULL DEFAULT 0);
INSERT INTO issues (id, title, status, type, priority, labels, parent_id, created_at, creator_session, deleted_at, sprint) VALUES
	('td-epic', 'search', 'open', 'epic', 'P1', '', '', '2026-07-12T20:00:00-04:00', 'ses_a', NULL, ''),
	('td-leak', 'index leaks a handle', 'in_progress', 'bug', 'P4', 'Index,perf', 'td-epic', '2026-07-12T20:01:29.996682-04:00', 'ses_a', NULL, 's1'),
	('td-schema', 'write the schema', 'open', 'task', 'P2', '', '', '2026-07-12T20:02:00-04:00', 'ses_b', NULL, ''),
	('td-old', 'first pass', 'closed', 'feature', 'P2', '', '', '2026-07-12T19:00:00-04:00', 'ses_b', NULL, ''),
	('td-gone', 'dropped', 'open', 'task', 'P2', '', '', '2026-07-12T19:30:00-04:00', 'ses_b', '2026-07-13T00:00:00-04:00', '');
INSERT INTO comments VALUES ('c1', 'td-leak', 'ses_b', 'same on main', '2026-07-13T05:02:25.718239-04:00');
INSERT INTO logs VALUES ('l1', 'td-leak', 'ses_a', 'token in the log', 'security', '2026-07-13T05:00:00-04:00'),
	('l2', 'td-leak', 'ses_a', 'found the leak', 'progress', '2026-07-13T05:01:00-04:00');
INSERT INTO handoffs VALUES ('h1', 'td-leak', 'ses_a', '["found it"]', '["fix it"]', '[]', '[]', '2026-07-13T05:24:29-04:00');
INSERT INTO issue_reviews VALUES ('r1', 'td-old', 'ses_c', 'approved', 'looks right', '2026-07-12T19:50:00-04:00', NULL, '');
INSERT INTO issue_dependencies VALUES ('d1', 'td-leak', 'td-schema', 'depends_on'), ('d2', 'td-schema', 'td-old', 'depends_on');
INSERT INTO notes VALUES ('n1', 'scratch', 'thoughts');
INSERT INTO boards VALUES ('b1', 'All Issues', 1);
`

func tdFixture(t *testing.T, dir string) (string, []byte) {
	t.Helper()
	path := filepath.Join(dir, ".todos", "issues.db")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(tdFixtureSchema); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return path, data
}

func decodeImport(t *testing.T, body map[string]any) ledger.WorkImportRequest {
	t.Helper()
	data, _ := json.Marshal(body)
	var req ledger.WorkImportRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	return req
}

func TestLedgerImportTDBringsOpenIssuesOverAsFoundWorkAndLeavesTDUntouched(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(_ string, body map[string]any) (int, any) {
		var result ledger.WorkImportResult
		req := decodeImport(t, body)
		for i, item := range req.Items {
			result.Imported = append(result.Imported, ledger.WorkImported{ImportedFrom: item.ImportedFrom, ItemID: "widget-W" + string(rune('1'+i))})
		}
		result.Deps = len(req.Deps)
		return http.StatusOK, result
	})
	dir, _ := os.Getwd()
	path, before := tdFixture(t, dir)

	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td"}) })
	if err != nil {
		t.Fatal(err)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(before, after) {
		t.Fatal("the import wrote to the td database")
	}
	if len(*calls) != 2 || (*calls)[0]["path"] != "/api/v1/ledger/import" {
		t.Fatalf("import requests = %v", *calls)
	}
	items, deps := decodeImport(t, (*calls)[0]).Items, decodeImport(t, (*calls)[1]).Deps
	var origins []string
	for _, item := range items {
		origins = append(origins, item.ImportedFrom)
	}
	if strings.Join(origins, " ") != "td:td-epic td:td-leak td:td-schema" {
		t.Fatalf("imported %v", origins)
	}
	epic, leak := items[0], items[1]
	if epic.Fields.Type != ledger.WorkFeature || strings.Join(epic.Fields.Labels, ",") != "epic" {
		t.Fatalf("epic = %+v", epic.Fields)
	}
	if leak.State != ledger.WorkOpen || leak.Fields.Type != ledger.WorkBug || leak.Fields.Priority != "P3" || leak.CreatedBy != "td:ses_a" ||
		leak.CreatedAt.Format("15:04:05") != "00:01:29" || leak.Handoff == nil || leak.Handoff.Remaining[0] != "fix it" {
		t.Fatalf("leak = %+v", leak)
	}
	var kinds []string
	for _, e := range leak.Timeline {
		kinds = append(kinds, e.Kind+":"+e.Text)
	}
	if got := strings.Join(kinds, " | "); got != `log:security: token in the log | log:found the leak | comment:same on main | handoff:done ["found it"], remaining ["fix it"], decisions [], uncertain []` {
		t.Fatalf("leak timeline = %s", got)
	}
	if len(deps) != 2 || deps[0] != (ledger.WorkImportDep{Item: "td:td-epic", On: "td:td-leak"}) || deps[1] != (ledger.WorkImportDep{Item: "td:td-leak", On: "td:td-schema"}) {
		t.Fatalf("deps = %+v", deps)
	}
	for _, want := range []string{"imported 3 items and 2 dependencies", "1 deleted issues", "1 closed issues", "1 in-progress or in-review", "1 issues with points, sprint",
		`1 "security" log entries`, "1 notes"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "saved boards") {
		t.Fatalf("the built-in board was reported as left over:\n%s", out)
	}
}

func TestLedgerImportTDAllBringsClosedIssuesAsDone(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.WorkImportResult{} })
	path, _ := tdFixture(t, t.TempDir())
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path, "--all"}) }); err != nil {
		t.Fatal(err)
	}
	items := decodeImport(t, (*calls)[0]).Items
	if len(items) != 4 || items[0].ImportedFrom != "td:td-old" || items[0].State != ledger.WorkDone || items[0].Reviewer != "td:ses_c" {
		t.Fatalf("items = %+v", items)
	}
	if deps := decodeImport(t, (*calls)[1]).Deps; len(deps) != 3 {
		t.Fatalf("deps = %+v", deps)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path, "--dry-run"}) }); err != nil || len(*calls) != 2 {
		t.Fatalf("dry run sent requests or failed: %v %d", err, len(*calls))
	}
}

func TestLedgerImportTDSplitsDependenciesAcrossRequestsUnderTheBodyLimit(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.WorkImportResult{} })
	path, _ := tdFixture(t, t.TempDir())
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	const n = 3000
	if _, err := db.Exec(`WITH RECURSIVE k(i) AS (SELECT 1 UNION ALL SELECT i+1 FROM k WHERE i < ?)
		INSERT INTO issues (id, title, created_at) SELECT printf('td-many-%05d', i), 'item', '2026-07-12T20:00:00-04:00' FROM k`, n); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO issue_dependencies SELECT 'dm' || a.id, a.id, b.id, 'depends_on' FROM issues a JOIN issues b
		ON a.id LIKE 'td-many-%' AND b.id = printf('td-many-%05d', CAST(substr(a.id, 9) AS INTEGER) + 1)`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path}) }); err != nil {
		t.Fatal(err)
	}
	depRequests, deps := 0, 0
	for _, call := range *calls {
		data, _ := json.Marshal(call)
		if len(data) > 140<<10 {
			t.Fatalf("an import request carries %d bytes", len(data))
		}
		if req := decodeImport(t, call); len(req.Deps) > 0 {
			depRequests, deps = depRequests+1, deps+len(req.Deps)
		}
	}
	if want := n - 1 + 2; deps != want || depRequests < 2 {
		t.Fatalf("sent %d dependencies in %d requests, want %d in several", deps, depRequests, want)
	}
}

func TestLedgerImportTDReadsAWALDatabaseWhileTDHoldsItOpen(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.WorkImportResult{} })
	path, _ := tdFixture(t, t.TempDir())
	td, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=wal_autocheckpoint(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer td.Close()
	td.SetMaxOpenConns(1)
	if _, err := td.Exec(`INSERT INTO issues (id, title, created_at) VALUES ('td-late', 'written while open', '2026-07-14T00:00:00-04:00')`); err != nil {
		t.Fatal(err)
	}
	main, _ := os.ReadFile(path)
	wal, err := os.ReadFile(path + "-wal")
	if err != nil || len(wal) == 0 {
		t.Fatalf("td's write is not in the WAL: %v", err)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path}) }); err != nil {
		t.Fatal(err)
	}
	var origins []string
	for _, item := range decodeImport(t, (*calls)[0]).Items {
		origins = append(origins, item.ImportedFrom)
	}
	if !strings.Contains(strings.Join(origins, " "), "td:td-late") {
		t.Fatalf("the import missed td's committed write in the WAL: %v", origins)
	}
	if after, _ := os.ReadFile(path); !bytes.Equal(main, after) {
		t.Fatal("the import changed the td database file")
	}
	if after, _ := os.ReadFile(path + "-wal"); !bytes.Equal(wal, after) {
		t.Fatal("the import changed td's WAL")
	}
	if _, err := td.Exec(`INSERT INTO issues (id, title, created_at) VALUES ('td-later', 'td keeps writing', '2026-07-14T00:01:00-04:00')`); err != nil {
		t.Fatalf("td cannot write after the import: %v", err)
	}
}

func TestLedgerImportTDDryRunLeavesOutWhatIsAlreadyImported(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.WorkImportResult{} })
	path, _ := tdFixture(t, t.TempDir())
	store := ledger.NewStore(app.registry().WriteDB(), app.registry().ReadDB())
	if _, err := store.ImportWork(ledger.WorkImportRequest{ProjectKey: "repo:github.com/acme/widget", SessionID: "s1",
		Items: []ledger.WorkImport{{ImportedFrom: "td:td-epic", Text: "search"}, {ImportedFrom: "td:td-leak", Text: "index leaks a handle"}},
		Deps:  []ledger.WorkImportDep{{Item: "td:td-epic", On: "td:td-leak"}}}, time.Now()); err != nil {
		t.Fatal(err)
	}
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path, "--dry-run"}) })
	if err != nil || !strings.Contains(out, "would import 1 items and 1 dependencies from "+path+" (2 already imported)") {
		t.Fatalf("dry run printed %q, err %v", out, err)
	}
	out, err = captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path, "--dry-run", "--json"}) })
	var plan struct {
		Items   []ledger.WorkImport    `json:"items"`
		Deps    []ledger.WorkImportDep `json:"deps"`
		Present []ledger.WorkImported  `json:"present"`
	}
	if err != nil || json.Unmarshal([]byte(out), &plan) != nil {
		t.Fatalf("dry run json %q, err %v", out, err)
	}
	if len(plan.Items) != 1 || plan.Items[0].ImportedFrom != "td:td-schema" || len(plan.Present) != 2 ||
		len(plan.Deps) != 1 || plan.Deps[0] != (ledger.WorkImportDep{Item: "td:td-leak", On: "td:td-schema"}) {
		t.Fatalf("dry run plan = %+v", plan)
	}
	if len(*calls) != 0 {
		t.Fatalf("a dry run sent %d requests", len(*calls))
	}
}

func TestLedgerImportTDParksDeferredIssuesForLaterUntilTheirDate(t *testing.T) {
	app, calls := ledgerWriteFixture(t, func(string, map[string]any) (int, any) { return http.StatusOK, ledger.WorkImportResult{} })
	path, _ := tdFixture(t, t.TempDir())
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE issues SET defer_until='2099-01-15' WHERE id='td-schema';
		UPDATE issues SET defer_until='2020-01-01' WHERE id='td-epic';
		UPDATE issues SET defer_until='2099-02-01T09:00:00Z', status='blocked' WHERE id='td-leak';
		UPDATE issues SET defer_until='2099-03-01' WHERE id='td-old'`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	out, err := captureStdout(t, func() error { return app.cmdLedger([]string{"import", "td", path, "--all"}) })
	if err != nil {
		t.Fatal(err)
	}
	byOrigin := map[string]ledger.WorkImport{}
	for _, item := range decodeImport(t, (*calls)[0]).Items {
		byOrigin[item.ImportedFrom] = item
	}
	schema := byOrigin["td:td-schema"]
	if schema.State != ledger.WorkParked || schema.Park == nil || schema.Park.Kind != ledger.ParkLater || schema.Park.Why != "deferred in td" || schema.Park.Revisit.In(time.Local).Format("2006-01-02") != "2099-01-15" {
		t.Fatalf("a deferred issue = %+v park %+v", schema, schema.Park)
	}
	if leak := byOrigin["td:td-leak"]; leak.State != ledger.WorkParked || leak.Park.Why != "blocked and deferred in td" || leak.Reason != "" || !leak.Park.Revisit.Equal(time.Date(2099, 2, 1, 9, 0, 0, 0, time.UTC)) {
		t.Fatalf("a blocked, deferred issue = %+v park %+v", leak, leak.Park)
	}
	if epic := byOrigin["td:td-epic"]; epic.State != ledger.WorkOpen || epic.Park != nil {
		t.Fatalf("an issue whose defer passed = %+v", epic)
	}
	if old := byOrigin["td:td-old"]; old.State != ledger.WorkDone || old.Park != nil {
		t.Fatalf("a closed, deferred issue = %+v", old)
	}
	if strings.Contains(out, "defer") {
		t.Fatalf("defer dates were reported as left behind:\n%s", out)
	}
}

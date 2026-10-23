package registry

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const (
	rekeyedBridge  = "session_01Rekeyed"
	collidedBridge = "session_01Collided"
	orphanBridge   = "session_01Orphan"
	launchedBridge = "session_01Launch"
	keptOrphan     = "session_01Kept"
	historyOrphan  = "session_01History"
)

var retiredIDs = []string{rekeyedBridge, collidedBridge, orphanBridge, launchedBridge}

func TestSchema14RekeysClaudeRowsToTheirConversation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	seedVersion13(t, r)
	assertEverySessionColumnSeeded(t, r.db)
	if _, err := r.db.Exec(`PRAGMA user_version=13`); err != nil {
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

	assertNoRetiredIDs(t, r.db)
	var version int
	if err := r.db.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil || version != schemaVersion {
		t.Fatalf("user_version=%d err=%v", version, err)
	}
	if owner, err := r.LookupAlias("kept"); err != nil || owner != keptOrphan || r.QueueCount(keptOrphan) != 1 {
		t.Fatalf("a referenced row without a transcript lost its alias or queue: owner=%q err=%v queue=%d", owner, err, r.QueueCount(keptOrphan))
	}
	if _, err := r.Session(historyOrphan); err != nil {
		t.Fatalf("a row named only by delivery history was deleted: %v", err)
	}
	if _, err := r.Session(orphanBridge); err == nil {
		t.Fatal("a row named nowhere else survived")
	}
	launched, err := r.Session("conversation-launch")
	if err != nil || launched.Transcript != "/claude/projects/-work/conversation-launch.jsonl" || !launched.HasLocal || launched.PID != 44 {
		t.Fatalf("launched=%+v err=%v", launched, err)
	}
	if owner, err := r.LookupAlias("launched"); err != nil || owner != "conversation-launch" {
		t.Fatalf("launched alias owner=%q err=%v", owner, err)
	}
	var receipt string
	if err := r.db.QueryRow(`SELECT CAST(receipt AS TEXT) FROM action_receipts WHERE idempotency_key='fixture-key'`).Scan(&receipt); err != nil || !strings.Contains(receipt, `"sessionId":"conversation-worker"`) {
		t.Fatalf("receipt=%s err=%v", receipt, err)
	}
	for alias, want := range map[string]string{"worker": "conversation-worker", "native": "conversation-native"} {
		if owner, err := r.LookupAlias(alias); err != nil || owner != want {
			t.Fatalf("alias %s owner=%q err=%v", alias, owner, err)
		}
	}
	if _, err := r.LookupAlias("collided"); err != sql.ErrNoRows {
		t.Fatalf("the colliding row's alias survived beside the conversation's own: %v", err)
	}
	worker, err := r.Session("conversation-worker")
	if err != nil || worker.Name != "worker" || worker.Transcript != "/claude/projects/-work/conversation-worker.jsonl" || worker.Bridge != "" {
		t.Fatalf("worker=%+v err=%v", worker, err)
	}
	if r.QueueCount("conversation-worker") != 1 || r.QueueCount("conversation-native") != 1 {
		t.Fatalf("queues worker=%d native=%d", r.QueueCount("conversation-worker"), r.QueueCount("conversation-native"))
	}
	rekeys, err := r.SessionRekeys()
	if err != nil || len(rekeys) != 3 || rekeys[rekeyedBridge] != "conversation-worker" || rekeys[collidedBridge] != "conversation-native" || rekeys[launchedBridge] != "conversation-launch" {
		t.Fatalf("rekeys=%v err=%v", rekeys, err)
	}
	removed := map[string]string{}
	rows, err := r.db.Query(`SELECT entity_id,payload FROM catalog_events WHERE type='session.removed'`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var entity string
		var payload []byte
		if err := rows.Scan(&entity, &payload); err != nil {
			t.Fatal(err)
		}
		var body struct {
			Reason    string `json:"reason"`
			RekeyedTo string `json:"rekeyedTo"`
		}
		if err := json.Unmarshal(payload, &body); err != nil || body.Reason != "rekeyed" {
			t.Fatalf("payload=%s err=%v", payload, err)
		}
		removed[entity] = body.RekeyedTo
	}
	rows.Close()
	if removed[rekeyedBridge] != "conversation-worker" || removed[collidedBridge] != "conversation-native" {
		t.Fatalf("removed=%v", removed)
	}
}

func seedVersion13(t *testing.T, r *Registry) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := r.db.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`CREATE TABLE api_action_reservations (idempotency_key TEXT PRIMARY KEY, session_id TEXT NOT NULL, action TEXT NOT NULL, message TEXT NOT NULL DEFAULT '', source_session_id TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'reserved', receipt TEXT NOT NULL DEFAULT '', created_at TEXT NOT NULL DEFAULT (datetime('now')), updated_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	exec(`CREATE TABLE daemon_event_items (dedupe_key TEXT PRIMARY KEY, event_id INTEGER NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))`)
	for _, s := range []surface.Session{
		{ID: rekeyedBridge, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "worker", Cwd: "/work", PID: 41, Transcript: "/claude/projects/-work/conversation-worker.jsonl", HasLocal: true},
		{ID: "conversation-native", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "native", Cwd: "/work", Transcript: "/claude/projects/-work/conversation-native.jsonl", HasLocal: true},
		{ID: collidedBridge, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "native again", Cwd: "/work", PID: 42, Transcript: "/claude/projects/-work/conversation-native.jsonl", HasLocal: true},
		{ID: orphanBridge, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "no transcript", PID: 43},
		{ID: keptOrphan, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "no transcript, still addressed"},
		{ID: historyOrphan, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "no transcript, in history"},
		{ID: "conversation-launch", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "launched", PID: 44},
		{ID: launchedBridge, Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "launched", Cwd: "/work", PID: 44, Transcript: "/claude/projects/-work/conversation-launch.jsonl", HasLocal: true},
		{ID: "codex-peer", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop},
	} {
		exec(`INSERT INTO sessions(id,surface,name,cwd,pid,transcript,has_local) VALUES(?,?,?,?,?,?,?)`, s.ID, string(s.Surface), s.Name, s.Cwd, s.PID, s.Transcript, b2i(s.HasLocal))
	}
	exec(`INSERT INTO sessions(id,surface,parent_session_id,root_session_id,forked_from) VALUES('child','claude',?,?,?)`, rekeyedBridge, rekeyedBridge, rekeyedBridge)
	exec(`INSERT INTO aliases(name,session_id) VALUES('worker',?),('native','conversation-native'),('collided',?),('kept',?),('launched','conversation-launch')`, rekeyedBridge, collidedBridge, keptOrphan)
	if err := r.QueueMessage(keptOrphan, "for the kept row"); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO action_receipts(idempotency_key,request_hash,status,receipt,created_at,updated_at) VALUES('fixture-key','hash','completed',?,datetime('now'),datetime('now'))`, []byte(`{"sessionId":"`+rekeyedBridge+`","status":"sent"}`))
	channel, err := r.CreateChannel("crew")
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO channel_members(channel_id,session_id) VALUES(?,?),(?,?)`, channel, rekeyedBridge, channel, collidedBridge)
	exec(`INSERT INTO coordinators(project_id,project_root,name,surface,cwd,changed_at_ms) VALUES('project-work','/work','work-lead','claude','/work',1)`)
	exec(`INSERT INTO coordinators(project_id,host_id,name,surface,session_id,changed_at_ms) VALUES('repo:github.com/example/work','h0123456789ab','remote-lead','claude',?,1)`, rekeyedBridge)
	exec(`INSERT INTO coordinator_history(project_id,action,name,session_id,previous_session_id,at_ms) VALUES('project-work','replaced','work-lead',?,?,1)`, rekeyedBridge, collidedBridge)
	exec(`INSERT INTO coordinator_replaced(session_id,project_id) VALUES(?,'project-work')`, rekeyedBridge)
	if _, err := r.AddRoute(rekeyedBridge, "codex-peer", ".*"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.AddRoute("codex-peer", collidedBridge, ".*"); err != nil {
		t.Fatal(err)
	}
	queued, err := r.QueueMessageWithOptions(rekeyedBridge, "for the worker", "", surface.SendOptions{SourceSessionID: collidedBridge})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.QueueMessageWithOptions(collidedBridge, "for the native row", "", surface.SendOptions{SourceSessionID: rekeyedBridge}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO attention_items(session_id,queue_id,reason,requested_action) VALUES(?,?,'held','retry')`, rekeyedBridge, queued)
	exec(`INSERT INTO delivery_history(kind,session_id,source_session_id) VALUES('send',?,?)`, rekeyedBridge, collidedBridge)
	exec(`INSERT INTO delivery_history(kind,session_id,error) VALUES('busy',?,'target is busy')`, historyOrphan)
	if _, err := r.RecordDeliveryIntent(DeliveryIntentInput{SenderSessionID: rekeyedBridge, TargetSessionID: collidedBridge, Message: "hello", Status: DeliveryIntentSubmitted, Evidence: surface.EvidenceSubmitted}); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO session_runtime(session_id) VALUES(?),(?),('conversation-native')`, rekeyedBridge, collidedBridge)
	exec(`INSERT INTO session_archive(session_id,archived_at_ms) VALUES(?,1)`, rekeyedBridge)
	exec(`INSERT INTO session_origins(session_id,via,creator_session_id) VALUES(?,'agent',?)`, rekeyedBridge, collidedBridge)
	exec(`INSERT INTO session_context(session_id,fingerprint,delivered_at_ms) VALUES(?,'f',1)`, rekeyedBridge)
	exec(`INSERT INTO limit_resumes(session_id,turn_id,refused_at_ms) VALUES(?,'refused',1)`, rekeyedBridge)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, id := range []string{rekeyedBridge, collidedBridge} {
		exec(`INSERT INTO session_journal(session_id,seq,kind,payload,observed_at,bytes,body_ref) VALUES(?,1,'item','{}',?,2,?)`, id, now, "body-"+id)
		exec(`INSERT INTO session_journal_state(session_id,next_seq) VALUES(?,2)`, id)
		exec(`INSERT INTO session_journal_bodies(ref,session_id,body,created_at) VALUES(?,?,'x',?)`, "body-"+id, id, now)
		exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,observed_at) VALUES(?,'{}','{}',?)`, id, now)
		exec(`INSERT INTO api_action_reservations(idempotency_key,session_id,action,source_session_id,receipt) VALUES(?,?,'send',?,?)`, "key-"+id, id, id, `{"sessionId":"`+id+`"}`)
		exec(`INSERT INTO daemon_events(event_type,entity_id,payload,created_at) VALUES('session.status',?,?,?)`, id, []byte(`{"sessionId":"`+id+`"}`), now)
		exec(`INSERT INTO daemon_event_items(dedupe_key,event_id) VALUES(?,1)`, "status:"+id)
	}
	exec(`INSERT INTO catalog_sessions(session_id,host_project,checkout,observed_at) VALUES('conversation-native','{}','{}',?)`, now)
	exec(`INSERT INTO session_journal_state(session_id,next_seq) VALUES('conversation-native',1)`)
}

type sessionColumn struct{ table, column string }

// sessionColumns lists every column that can hold a session id, read from
// the live schema so a table added later is covered without editing this test.
func sessionColumns(t *testing.T, db *sql.DB) []sessionColumn {
	t.Helper()
	rows, err := db.Query(`SELECT m.name,p.name FROM sqlite_master m, pragma_table_info(m.name) p WHERE m.type='table' AND m.name NOT IN ('session_rekeys','sqlite_sequence') AND (p.name LIKE '%session%' OR p.name IN ('entity_id','forked_from') OR (m.name='sessions' AND p.name='id') OR (m.name LIKE '%event%' AND p.name IN ('dedupe_key','payload')) OR p.name='receipt')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []sessionColumn
	for rows.Next() {
		var c sessionColumn
		if err := rows.Scan(&c.table, &c.column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, c)
	}
	if len(columns) == 0 {
		t.Fatal("no session columns found")
	}
	return columns
}

func retiredIDMatch(column string) (string, []any) {
	clauses := make([]string, len(retiredIDs))
	args := make([]any, len(retiredIDs))
	for i, id := range retiredIDs {
		clauses[i] = `instr(CAST(` + column + ` AS TEXT),?)>0`
		args[i] = id
	}
	return `(` + strings.Join(clauses, " OR ") + `)`, args
}

func assertEverySessionColumnSeeded(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, c := range sessionColumns(t, db) {
		match, args := retiredIDMatch(c.column)
		var seeded bool
		if err := db.QueryRow(`SELECT EXISTS(SELECT 1 FROM `+c.table+` WHERE `+match+`)`, args...).Scan(&seeded); err != nil {
			t.Fatal(err)
		}
		if !seeded {
			t.Errorf("fixture holds no bridge id in %s.%s; seed it so the rekey is proven to cover it", c.table, c.column)
		}
	}
}

func assertNoRetiredIDs(t *testing.T, db *sql.DB) {
	t.Helper()
	for _, c := range sessionColumns(t, db) {
		match, args := retiredIDMatch(c.column)
		query := `SELECT COUNT(*) FROM ` + c.table + ` WHERE ` + match
		if c.table == "catalog_events" {
			query += ` AND NOT (type='session.removed' AND json_extract(CAST(payload AS TEXT),'$.reason')='rekeyed')`
		}
		var count int
		if err := db.QueryRow(query, args...).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Errorf("%s.%s still holds %d retired ids", c.table, c.column, count)
		}
	}
}

package registry

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

const endedHost = "h0000000000e1"

func pairEndedHost(t *testing.T, r *Registry) {
	t.Helper()
	if _, err := r.AddHost(PairedHost{ID: endedHost, Name: "mac", Endpoint: "https://mac.example.ts.net:7412", Address: "100.64.0.3", Token: "ahd_synthetic"}); err != nil {
		t.Fatal(err)
	}
}

func mirrorCodexThread(t *testing.T, r *Registry, session surface.Session, projection string) {
	t.Helper()
	if _, _, err := r.ApplyHostCatalogRow(endedHost, HostCatalogRow{Session: session, EntityID: endedHost + "::" + session.ID, Projection: json.RawMessage(projection)}); err != nil {
		t.Fatal(err)
	}
}

func endedOf(t *testing.T, r *Registry, id string) bool {
	t.Helper()
	var ended bool
	if err := r.read.QueryRow(`SELECT ended FROM sessions WHERE id=?`, id).Scan(&ended); err != nil {
		t.Fatal(err)
	}
	return ended
}

func TestEveryCatalogReadCarriesWhetherASessionEnded(t *testing.T) {
	r := openTestRegistry(t)
	pairEndedHost(t, r)
	recordCodexThread(t, r, codexThread("local-ended", "", "", 0, true))
	recordCodexThread(t, r, codexThread("local-held", "", "", 0, false))
	mirrorCodexThread(t, r, codexThread("remote-ended", "", "", 0, true), `{"id":"remote-ended"}`)
	want := map[string]bool{"local-ended": true, "local-held": false, "remote-ended": true}

	check := func(read string, sessions []CatalogSessionState) {
		t.Helper()
		seen := map[string]bool{}
		for _, state := range sessions {
			if ended, listed := want[state.Session.ID]; listed {
				seen[state.Session.ID] = true
				if state.Session.Ended != ended {
					t.Errorf("%s reads %s as ended=%v, want %v", read, state.Session.ID, state.Session.Ended, ended)
				}
			}
		}
		if len(seen) == 0 {
			t.Errorf("%s read none of the fixture rows", read)
		}
	}
	snapshot, _, err := r.CatalogMirrorSnapshot(nil)
	if err != nil {
		t.Fatal(err)
	}
	check("the snapshot a host sends its peers", snapshot.Sessions)
	since := int64(0)
	snapshot, _, err = r.CatalogMirrorSnapshot(&since)
	if err != nil {
		t.Fatal(err)
	}
	check("the incremental snapshot a host sends its peers", snapshot.Sessions)
	current, err := r.CatalogSnapshot()
	if err != nil {
		t.Fatal(err)
	}
	check("the current snapshot", current.Sessions)
	page, err := r.CatalogSnapshotPage(CatalogPageRequest{Scope: "all", Limit: 10, Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	check("a catalog page", page.Sessions)
	active, err := r.CatalogActiveSessions(time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	check("the active sessions", active)
	mirrored, err := r.HostCatalogRows(endedHost)
	if err != nil {
		t.Fatal(err)
	}
	check("a host's mirrored rows", mirrored)
	unretired, err := r.HostUnretiredCatalogRows(endedHost)
	if err != nil {
		t.Fatal(err)
	}
	check("a host's unretired mirrored rows", unretired)
}

func TestRewritingAMirroredRowFromItsStoredStateKeepsItEnded(t *testing.T) {
	r := openTestRegistry(t)
	pairEndedHost(t, r)
	mirrorCodexThread(t, r, codexThread("remote-ended", "", "", 0, true), `{"id":"remote-ended"}`)
	rows, err := r.HostUnretiredCatalogRows(endedHost)
	if err != nil || len(rows) != 1 {
		t.Fatalf("rows=%d err=%v", len(rows), err)
	}
	if _, _, err := r.ApplyHostCatalogRow(endedHost, HostCatalogRow{Session: rows[0].Session, EntityID: endedHost + "::remote-ended", Projection: json.RawMessage(`{"id":"remote-ended","unavailableReason":"host mac is unreachable"}`), UnavailableReason: "host mac is unreachable"}); err != nil {
		t.Fatal(err)
	}
	if !endedOf(t, r, "remote-ended") {
		t.Fatal("rewriting a mirrored row from its own stored state cleared ended")
	}
}

func TestAMirroredRowItsHostSendsAgainStaysRetiredUntilItIsActiveAgain(t *testing.T) {
	r := openTestRegistry(t)
	pairEndedHost(t, r)
	thread := codexThread("remote-ended", "", "", 0, true)
	mirrorCodexThread(t, r, thread, `{"id":"remote-ended"}`)
	codexSweep(t, r)
	if !isRetired(t, r, "remote-ended") {
		t.Fatal("fixture: an old, ended mirrored row should retire")
	}
	renamed := thread
	renamed.Name = "renamed on its host"
	mirrorCodexThread(t, r, renamed, `{"id":"remote-ended","name":"renamed on its host"}`)
	if !isRetired(t, r, "remote-ended") {
		t.Fatal("a host sending an ended, old row again un-retired it")
	}
	active := renamed
	active.LastActive = time.Now().Add(-time.Minute)
	mirrorCodexThread(t, r, active, `{"id":"remote-ended","name":"renamed on its host","active":"recent"}`)
	if isRetired(t, r, "remote-ended") {
		t.Fatal("a mirrored row with new activity stayed retired")
	}
}

func TestAHostsRetiredRowsRetireOnItsMirror(t *testing.T) {
	r := openTestRegistry(t)
	pairEndedHost(t, r)
	stale := codexThread("stale", "", "", 0, true)
	stale.Ended = false
	mirrorCodexThread(t, r, stale, `{"id":"stale"}`)
	mirrorCodexThread(t, r, codexThread("kept", "", "", 0, false), `{"id":"kept"}`)
	recordCodexThread(t, r, codexThread("local", "", "", 0, false))
	if endedOf(t, r, "stale") || isRetired(t, r, "stale") {
		t.Fatal("fixture: the stale mirrored copy should be unended and unretired")
	}
	if err := r.RetireHostRows(endedHost, []string{"stale", "local", "absent"}); err != nil {
		t.Fatal(err)
	}
	if !endedOf(t, r, "stale") || !isRetired(t, r, "stale") {
		t.Fatal("a row its host lists as retired stayed current on the mirror")
	}
	if endedOf(t, r, "local") || isRetired(t, r, "local") {
		t.Fatal("a host's retired list reached a local session")
	}
	if endedOf(t, r, "kept") || isRetired(t, r, "kept") {
		t.Fatal("a row its host did not list retired")
	}
	codexSweep(t, r, "local")
	if !isRetired(t, r, "stale") {
		t.Fatal("the next pass un-retired a row its host retired")
	}
}

func TestEverySessionColumnTheUpsertWritesIsReadAndCompared(t *testing.T) {
	r := openTestRegistry(t)
	rows, err := r.read.Query(`SELECT name FROM pragma_table_info('sessions')`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	written := map[string]bool{}
	for _, column := range sessionWriteColumns {
		written[column.name] = true
	}
	notRegistered := map[string]bool{"id": true, "host_id": true, "updated_at": true, "registered_at": true, "pinned_at_ms": true, "stopped_at_ms": true, "snoozed_at_ms": true, "snoozed_until_ms": true, "snooze_wake": true, "entrypoint": true}
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		if written[column] || notRegistered[column] {
			continue
		}
		t.Errorf("sessions.%s is neither written by registering a session nor listed as written elsewhere; add it to sessionWriteColumns or notRegistered", column)
	}
	reader := sessionColumnsSQL("s.status")
	for _, column := range sessionWriteColumns {
		if column.name == "parent_session_id" || column.name == "root_session_id" || column.name == "subagent_depth" || column.name == "agent_nickname" || column.name == "agent_role" {
			if !strings.Contains(sessionSubagentSelect, "s."+column.name) {
				t.Errorf("the subagent columns the readers select miss %s", column.name)
			}
			continue
		}
		if column.name != "status" && !strings.Contains(reader, "s."+column.name+",") && !strings.HasSuffix(reader, "s."+column.name) {
			t.Errorf("whole-session readers do not select %s, which registering a session writes", column.name)
		}
	}
}

func TestTheUnchangedCheckSeesEveryChangeRegisteringWouldWrite(t *testing.T) {
	base := surface.Session{ID: "s", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop, Name: "n", Cwd: "/w", Status: surface.StatusIdle, Source: "vscode", Transport: "desktop", LastActive: time.UnixMilli(1_700_000_000_000)}
	for _, change := range []struct {
		name   string
		mutate func(*surface.Session)
	}{
		{"ended", func(s *surface.Session) { s.Ended = true }},
		{"status", func(s *surface.Session) { s.Status = surface.StatusNotLoaded }},
		{"name", func(s *surface.Session) { s.Name = "renamed" }},
		{"cwd", func(s *surface.Session) { s.Cwd = "/elsewhere" }},
		{"pid", func(s *surface.Session) { s.PID = 42 }},
		{"transcript", func(s *surface.Session) { s.Transcript = "/t.jsonl" }},
		{"has local", func(s *surface.Session) { s.HasLocal = true }},
		{"configured model", func(s *surface.Session) { s.ConfiguredModel = "model" }},
		{"bridge", func(s *surface.Session) { s.Bridge = "bridge" }},
		{"forked from", func(s *surface.Session) { s.ForkedFrom = "origin" }},
		{"last active", func(s *surface.Session) { s.LastActive = s.LastActive.Add(time.Second) }},
		{"subagent parent", func(s *surface.Session) { s.Subagent = &surface.Subagent{ParentID: "p", RootID: "p", Depth: 1} }},
		{"runtime kind", func(s *surface.Session) { s.RuntimeKind = surface.RuntimeCodexAppServer }},
	} {
		t.Run(change.name, func(t *testing.T) {
			r := openTestRegistry(t)
			if err := r.RegisterSession(base); err != nil {
				t.Fatal(err)
			}
			if same := matches(t, r, base); !same {
				t.Fatal("a registered session does not match itself")
			}
			changed := base
			change.mutate(&changed)
			if matches(t, r, changed) {
				t.Fatalf("the unchanged check misses a %s change registering would write", change.name)
			}
			if err := r.RegisterSession(changed); err != nil {
				t.Fatal(err)
			}
			if !matches(t, r, changed) {
				t.Fatalf("after registering a %s change the session does not match what was written", change.name)
			}
		})
	}
}

func matches(t *testing.T, r *Registry, s surface.Session) bool {
	t.Helper()
	tx, err := r.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	same, err := sessionRowMatchesTx(tx, s)
	if err != nil {
		t.Fatal(err)
	}
	return same
}

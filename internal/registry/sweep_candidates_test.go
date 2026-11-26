package registry

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func seedSweepSessions(t *testing.T, r *Registry, since time.Time) {
	t.Helper()
	edge := since.UnixMilli()
	for _, s := range []struct {
		id, kind, host string
		lastActive     int64
	}{
		{"never", "claude", "", 0},
		{"long-ago", "claude", "", edge - 86_400_000},
		{"just-before", "codex", "", edge},
		{"just-after", "claude", "", edge + 1},
		{"recent-codex", "codex", "", edge + 5_000},
		{"recent-claude", "claude", "", edge + 9_000},
		{"future", "claude", "", edge + 3_600_000},
		{"remote-recent", "claude", "h-other", edge + 9_000},
		{"remote-codex", "codex", "h-other", edge + 9_000},
	} {
		if _, err := r.db.Exec(`INSERT INTO sessions(id,surface,host_id,last_active_ms,transcript) VALUES(?,?,?,?,?)`, s.id, s.kind, s.host, s.lastActive, "/t/"+s.id+".jsonl"); err != nil {
			t.Fatal(err)
		}
	}
}

func keep(sessions []surface.Session, ok func(surface.Session) bool) []surface.Session {
	out := []surface.Session{}
	for _, s := range sessions {
		if ok(s) {
			out = append(out, s)
		}
	}
	return out
}

func TestNarrowedSessionListsPickWhatTheFullListPicked(t *testing.T) {
	r := openTestRegistry(t)
	since := time.UnixMilli(time.Now().Add(-24 * time.Hour).UnixMilli()).Add(400 * time.Microsecond)
	seedSweepSessions(t, r, since)
	all, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	active, err := r.ListSessionsActiveSince(since)
	if err != nil {
		t.Fatal(err)
	}
	if want := keep(all, func(s surface.Session) bool { return !s.LastActive.IsZero() && !s.LastActive.Before(since) }); !reflect.DeepEqual(active, want) {
		t.Fatalf("active since %v:\n got %v\nwant %v", since, ids(active), ids(want))
	}
	claude, err := r.ListSessionsOf(surface.KindClaude)
	if err != nil {
		t.Fatal(err)
	}
	if want := keep(all, func(s surface.Session) bool { return s.Surface == surface.KindClaude }); !reflect.DeepEqual(claude, want) {
		t.Fatalf("claude sessions:\n got %v\nwant %v", ids(claude), ids(want))
	}
	if len(active) == 0 || len(active) == len(all) || len(claude) == 0 || len(claude) == len(all) {
		t.Fatalf("seed does not split the list: all=%d active=%d claude=%d", len(all), len(active), len(claude))
	}
}

func ids(sessions []surface.Session) []string {
	out := make([]string, len(sessions))
	for i, s := range sessions {
		out[i] = s.ID
	}
	return out
}

const fullScanWatchedForTurnEnd = `
	SELECT l.id FROM sessions l
	WHERE l.host_id='' AND (SELECT te.completed_turn_id='' AND te.started_at_ms>=? FROM turn_ends te WHERE te.session_id=l.id ORDER BY te.started_at_ms DESC LIMIT 1)`

func TestWatchedSessionsFromTurnEndsMatchTheFullSessionScan(t *testing.T) {
	r := openTestRegistry(t)
	now := time.Now()
	cutoff := now.Add(-TurnEndWatch).UnixMilli()
	seedSweepSessions(t, r, now.Add(-time.Hour))
	for _, row := range []struct {
		session   string
		started   int64
		completed string
	}{
		{"recent-claude", cutoff + 10, ""},
		{"recent-claude", cutoff + 20, ""},
		{"recent-codex", cutoff + 10, ""},
		{"recent-codex", cutoff + 20, "done"},
		{"just-after", cutoff - 1, ""},
		{"future", cutoff, ""},
		{"never", cutoff + 5, "done"},
		{"never", cutoff - 50, ""},
		{"remote-recent", cutoff + 10, ""},
	} {
		if _, err := r.db.Exec(`INSERT INTO turn_ends(session_id,started_at_ms,completed_turn_id) VALUES(?,?,?)`, row.session, row.started, row.completed); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.QueueMessageWithKey("long-ago", "queued", "k1"); err != nil {
		t.Fatal(err)
	}
	work := awaitingTurnArgs(now)
	args := append(append(append([]any{}, work...), work...), cutoff)
	fullScan := strings.Replace(watchedSessionsQuery, watchedForTurnEnd, fullScanWatchedForTurnEnd, 1)
	if fullScan == watchedSessionsQuery {
		t.Fatal("the reference query was not built")
	}
	got, want := watchedRows(t, r, watchedSessionsQuery, args), watchedRows(t, r, fullScan, args)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("watched:\n got %v\nwant %v", got, want)
	}
	if strings.Join(want, ",") != "claude/future/turn,claude/long-ago/work,claude/recent-claude/turn" {
		t.Fatalf("seed covers %v", want)
	}
	for _, step := range queryPlan(t, r, watchedForTurnEnd, cutoff) {
		if strings.HasPrefix(step, "SCAN te") {
			t.Fatalf("open turns are found by scanning every turn: %q", step)
		}
	}
}

func watchedRows(t *testing.T, r *Registry, query string, args []any) []string {
	t.Helper()
	rows, err := r.read.Query(query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, kind string
		var armed, turnOnly bool
		if err := rows.Scan(&id, &kind, &armed, &turnOnly); err != nil {
			t.Fatal(err)
		}
		why := "work"
		if turnOnly {
			why = "turn"
		}
		out = append(out, kind+"/"+id+"/"+why)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSessionsUsingAFolderPickWhatTheFullListPicked(t *testing.T) {
	r := openTestRegistry(t)
	root := "/w/repo/.worktrees/task"
	for _, s := range []struct{ id, host, cwd string }{
		{"at-root", "", root},
		{"inside", "", root + "/sub/dir"},
		{"sibling", "", root + "-2"},
		{"sibling-inside", "", root + "2/sub"},
		{"parent", "", "/w/repo"},
		{"owner", "", "/elsewhere"},
		{"remote-inside", "h-other", root + "/sub"},
		{"no-folder", "", ""},
	} {
		if _, err := r.db.Exec(`INSERT INTO sessions(id,surface,host_id,cwd,transcript) VALUES(?,?,?,?,?)`, s.id, "claude", s.host, s.cwd, "/t/"+s.id+".jsonl"); err != nil {
			t.Fatal(err)
		}
	}
	all, err := r.ListSessions(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, owner := range []string{"", "owner"} {
		using, err := r.ListSessionsUsing(root, owner)
		if err != nil {
			t.Fatal(err)
		}
		want := keep(all, func(s surface.Session) bool {
			return s.ID == owner || s.Cwd == root || strings.HasPrefix(s.Cwd, root+"/")
		})
		if !reflect.DeepEqual(using, want) {
			t.Fatalf("owner %q: got %v, want %v", owner, ids(using), ids(want))
		}
	}
}

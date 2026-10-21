package daemon

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestStateReportsRekeyedIDsAndBranchParents(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "registry.db")
	r, err := registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := surface.Session{ID: "session_upgraded", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "upgraded", Status: surface.StatusIdle, Transcript: "/fixture/.claude/projects/fixture/conversation-upgraded.jsonl", HasLocal: true}
	branch := surface.Session{ID: "conversation-branch", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Name: "branch", Status: surface.StatusIdle, ForkedFrom: "conversation-upgraded", Transcript: "/fixture/.claude/projects/fixture/conversation-branch.jsonl", HasLocal: true}
	for _, session := range []surface.Session{upgraded, branch} {
		if err := r.RegisterSession(session); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.SetAlias("worker", upgraded.ID); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`PRAGMA user_version=13`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	r, err = registry.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })

	fake := &daemonSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}, observations: map[string]*surface.TurnObservation{}}
	state := readDashboardState(t, dashboardRouter(newTestDaemon(t, r, []surface.Surface{fake})))
	if state.Rekeyed[upgraded.ID] != "conversation-upgraded" {
		t.Fatalf("rekeyed=%v", state.Rekeyed)
	}
	rows := map[string]dashboardSession{}
	for _, session := range state.Sessions {
		rows[session.ID] = session
	}
	if _, found := rows[upgraded.ID]; found {
		t.Fatalf("the retired id is still listed: %+v", state.Sessions)
	}
	if row := rows["conversation-upgraded"]; row.Alias != "worker" {
		t.Fatalf("upgraded row=%+v", row)
	}
	if row := rows["conversation-branch"]; row.ForkedFrom != "conversation-upgraded" || row.Alias != "" {
		t.Fatalf("branch row=%+v", row)
	}
}

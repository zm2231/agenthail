package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

type moverSurface struct {
	*cliSurface
	busy  map[string]string
	moved []string
	to    string
}

func (m *moverSurface) MoveSession(_ context.Context, sess *surface.Session, request surface.MoveRequest) (*surface.MoveResult, error) {
	if reason := m.busy[sess.ID]; reason != "" {
		return nil, surface.DeliveryTerminal(&surface.MoveBusyError{Session: sess.ID, Reason: reason}, surface.DeliveryInvalidRequest)
	}
	m.moved = append(m.moved, sess.ID+"->"+request.To.Account)
	moved := *sess
	moved.Transcript = filepath.Join(m.to, "projects", "p", sess.ID+".jsonl")
	return &surface.MoveResult{Session: &moved, FromRoot: "/from", ToRoot: m.to, Copied: true}, nil
}

func moveCLIFixture(t *testing.T) (*App, *registry.Registry, *moverSurface, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	spare := filepath.Join(home, "spare")
	if err := os.MkdirAll(filepath.Join(home, ".agenthail"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(spare, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".agenthail", "config.json"), []byte(`{"claudeConfigDirs":["`+spare+`"]}`), 0600); err != nil {
		t.Fatal(err)
	}
	fake := &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{}}
	mover := &moverSurface{cliSurface: fake, busy: map[string]string{}, to: spare}
	app, store := cliFixture(t, fake.cliSurface())
	app.Surfaces = []SurfaceEntry{{Name: "claude", Surface: mover}}
	return app, store, mover, spare
}

func (f *cliSurface) cliSurface() *cliSurface { return f }

func TestThreadMoveMovesOneSessionAndReRegistersIt(t *testing.T) {
	app, store, mover, spare := moveCLIFixture(t)
	session := surface.Session{ID: "c1", Name: "one", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Transcript: "/from/projects/p/c1.jsonl"}
	mover.sessions["c1"] = session
	if err := store.RegisterSession(session); err != nil {
		t.Fatal(err)
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"thread", "move", "c1", "--account", "spare", "--json"}) })
	if err != nil {
		t.Fatalf("output=%s err=%v", output, err)
	}
	if len(mover.moved) != 1 || mover.moved[0] != "c1->spare" {
		t.Fatalf("moved=%v", mover.moved)
	}
	stored, err := store.Session("c1")
	if err != nil || stored.Transcript != filepath.Join(spare, "projects", "p", "c1.jsonl") {
		t.Fatalf("stored=%+v err=%v", stored, err)
	}
	for _, args := range [][]string{{"thread", "move", "c1"}, {"thread", "adopt", "c1", "--account", "spare"}, {"thread", "move", "c1", "--account", "spare", "--wait", "soon"}} {
		if err := app.Run(args); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
}

func TestThreadMoveAllMovesIdleSessionsAndReportsTheBusyOnes(t *testing.T) {
	app, store, mover, spare := moveCLIFixture(t)
	app.catalogDaemonRunning = func() bool { return true }
	mover.busy["busy"] = "a turn is running"
	observedAt := time.Now().UTC()
	for _, row := range []struct {
		session    surface.Session
		projection string
	}{
		{surface.Session{ID: "idle"}, `{"current":true}`},
		{surface.Session{ID: "old"}, `{"current":false}`},
		{surface.Session{ID: "busy"}, `{"current":true}`},
		{surface.Session{ID: "there"}, `{"current":true,"claudeAccount":{"name":"spare","root":"` + spare + `","label":"spare"}}`},
		{surface.Session{ID: "hidden"}, `{"current":true,"archived":true}`},
		{surface.Session{ID: "child", Subagent: &surface.Subagent{ParentID: "idle", RootID: "idle", Depth: 1}}, `{"current":true}`},
	} {
		row.session.Surface, row.session.RuntimeKind, row.session.Name, row.session.LastActive = surface.KindClaude, surface.RuntimeClaudeCLI, row.session.ID, observedAt
		if _, _, err := store.RecordCatalogSession(registry.CatalogSessionState{Session: row.session, Project: json.RawMessage(`null`), Checkout: json.RawMessage(`{}`), ObservedAt: observedAt, ProjectionFingerprint: row.projection}, registry.CatalogEvent{DedupeKey: "session:" + row.session.ID, Type: "session.upserted", EntityID: row.session.ID, Payload: json.RawMessage(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	output, err := captureStdout(t, func() error { return app.Run([]string{"thread", "move", "--all", "--account", "spare", "--json"}) })
	if err != nil {
		t.Fatalf("output=%s err=%v", output, err)
	}
	var report struct {
		OK    bool `json:"ok"`
		Moves []struct {
			Session string `json:"session"`
			Skipped string `json:"skipped"`
		} `json:"moves"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil || !report.OK {
		t.Fatalf("output=%s err=%v", output, err)
	}
	got := map[string]string{}
	for _, move := range report.Moves {
		got[move.Session] = move.Skipped
	}
	if len(got) != 3 || got["busy"] != "a turn is running" || got["idle"] != "" || got["old"] != "" {
		t.Fatalf("moves=%+v", report.Moves)
	}
	if strings.Join(mover.moved, ",") == "" || strings.Contains(strings.Join(mover.moved, ","), "there") || strings.Contains(strings.Join(mover.moved, ","), "child") || strings.Contains(strings.Join(mover.moved, ","), "hidden") {
		t.Fatalf("moved=%v", mover.moved)
	}
}

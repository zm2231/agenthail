package surfaces

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/claudehost/claudehosttest"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces/claudetest"
)

type rootMoveFixture struct {
	host    *claudehosttest.Fixture
	c       *Claude
	second  string
	session *surface.Session
	cwd     string
}

func newRootMoveFixture(t *testing.T) rootMoveFixture {
	t.Helper()
	fixture := claudetest.New(t)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	host := claudehosttest.New(t, fixture.Home)
	host.Start(t)
	second := filepath.Join(host.Home, ".work", ".claude")
	configureClaudeRoots(t, host.Home, second)
	c := NewClaude(nil, host.Home)
	session, _, err := c.StartSession(context.Background(), surface.SessionStartOptions{Message: "build it", Cwd: host.Cwd})
	if err != nil {
		t.Fatal(err)
	}
	waitForTranscript(t, c, session.ID)
	cwd, err := filepath.EvalSymlinks(host.Cwd)
	if err != nil {
		t.Fatal(err)
	}
	project := `{"projects":{"` + cwd + `":{"hasTrustDialogAccepted":true,"allowedTools":["Bash"],"mcpServers":{"docs":{"command":"docs-mcp"},"search":{"command":"search-mcp"}}}}}`
	writeFixtureFile(t, filepath.Join(host.Home, ".claude.json"), project)
	writeFixtureFile(t, filepath.Join(second, ".claude.json"), `{"numStartups":1}`)
	return rootMoveFixture{host: host, c: c, second: second, session: session, cwd: cwd}
}

func (f rootMoveFixture) transcriptIn(t *testing.T, root string) bool {
	t.Helper()
	return f.projectIn(root) != ""
}

func (f rootMoveFixture) projectIn(root string) string {
	found, _ := filepath.Glob(filepath.Join(root, "projects", "*", f.session.ID+".jsonl"))
	if len(found) != 1 {
		return ""
	}
	return filepath.Base(filepath.Dir(found[0]))
}

func (f rootMoveFixture) prepare(t *testing.T) *surface.MoveOut {
	t.Helper()
	out, err := f.c.PrepareMove(context.Background(), f.session, surface.AdoptRequest{Wait: claudeAdoptStopTimeout})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(out.Release)
	return out
}

func TestARootMoveResumesTheSessionUnderTheTargetWithItsProjectConfig(t *testing.T) {
	f := newRootMoveFixture(t)
	primary := filepath.Join(f.host.Home, ".claude")
	writeFixtureFile(t, filepath.Join(primary, "tasks", f.session.ID, "1.json"), `{"id":"1"}`)
	out := f.prepare(t)
	moveID := uuid.NewString()

	resumed, err := f.c.MoveToRoot(context.Background(), out, moveID, "work")
	if err != nil {
		t.Fatal(err)
	}
	if resumed.ID != f.session.ID || resumed.PID <= 0 {
		t.Fatalf("resumed=%+v", resumed)
	}
	if f.transcriptIn(t, primary) || !f.transcriptIn(t, f.second) {
		t.Fatal("the transcript did not move to the target root")
	}
	if _, err := os.Stat(filepath.Join(f.second, "tasks", f.session.ID, "1.json")); err != nil {
		t.Fatalf("tasks did not move: %v", err)
	}
	if _, err := os.Stat(filepath.Join(primary, claudeMovedDir, f.session.ID, moveID)); !os.IsNotExist(err) {
		t.Fatalf("the move left its archive: %v", err)
	}
	spawns := f.host.Spawns(t, f.session.ID)
	if last := spawns[len(spawns)-1]; last.ConfigDir == nil || *last.ConfigDir != f.second {
		t.Fatalf("resume ran in %v, want %s", last.ConfigDir, f.second)
	}
	source, _ := os.ReadFile(filepath.Join(f.host.Home, ".claude.json"))
	target, _ := os.ReadFile(filepath.Join(f.second, ".claude.json"))
	key := "projects." + gjsonKey(f.cwd)
	if !gjson.GetBytes(target, key+".hasTrustDialogAccepted").Bool() || gjson.GetBytes(target, key+".mcpServers").Raw != gjson.GetBytes(source, key+".mcpServers").Raw {
		t.Fatalf("the target's project entry is %s, want the source's %s", gjson.GetBytes(target, key).Raw, gjson.GetBytes(source, key).Raw)
	}
	if gjson.GetBytes(target, "numStartups").Int() != 1 {
		t.Fatalf("the target's own config changed: %s", target)
	}
}

func TestARootMoveTheTargetCannotTakeLeavesEverythingWhereItWas(t *testing.T) {
	f := newRootMoveFixture(t)
	primary := filepath.Join(f.host.Home, ".claude")
	writeFixtureFile(t, filepath.Join(f.second, "tasks", f.session.ID, "old.json"), `{}`)
	out := f.prepare(t)

	if _, err := f.c.MoveToRoot(context.Background(), out, uuid.NewString(), "work"); err == nil {
		t.Fatal("a move onto a copy in the target root went ahead")
	}
	if !f.transcriptIn(t, primary) || f.transcriptIn(t, f.second) {
		t.Fatal("a refused move did not leave the transcript in its root")
	}
	if target, _ := os.ReadFile(filepath.Join(f.second, ".claude.json")); string(target) != `{"numStartups":1}` {
		t.Fatalf("a refused move left the target config changed: %s", target)
	}
	if _, err := f.c.MoveToRoot(context.Background(), out, uuid.NewString(), "default"); err == nil {
		t.Fatal("a move to the root the session is in went ahead")
	}
}

func TestRecoveringARootMoveFinishesAPlacedOneAndReturnsAnUnplacedOne(t *testing.T) {
	f := newRootMoveFixture(t)
	primary := filepath.Join(f.host.Home, ".claude")
	project := f.projectIn(primary)
	writeFixtureFile(t, filepath.Join(primary, "projects", project, f.session.ID, "subagents", "a.jsonl"), "{}\n")
	out, err := f.c.PrepareMove(context.Background(), f.session, surface.AdoptRequest{Wait: claudeAdoptStopTimeout})
	if err != nil {
		t.Fatal(err)
	}
	out.Release()

	unplaced := uuid.NewString()
	if err := f.c.ArchiveMove(f.session.ID, unplaced); err != nil {
		t.Fatal(err)
	}
	archive := f.c.movedDir(primary, f.session.ID, unplaced)
	if err := os.MkdirAll(filepath.Join(f.second, "projects", project), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(archive, f.session.ID), filepath.Join(f.second, "projects", project, f.session.ID)); err != nil {
		t.Fatal(err)
	}
	moved, err := f.c.RecoverRootMove(context.Background(), f.session.ID, unplaced, "work")
	if err != nil || moved {
		t.Fatalf("an unplaced move recovered as moved=%v: %v", moved, err)
	}
	if !f.transcriptIn(t, primary) {
		t.Fatal("an unplaced move did not come back")
	}
	if _, err := os.Stat(filepath.Join(primary, "projects", project, f.session.ID, "subagents", "a.jsonl")); err != nil {
		t.Fatalf("a part already placed did not come back: %v", err)
	}

	placed := uuid.NewString()
	if err := f.c.ArchiveMove(f.session.ID, placed); err != nil {
		t.Fatal(err)
	}
	archive = f.c.movedDir(primary, f.session.ID, placed)
	if err := os.Rename(filepath.Join(archive, f.session.ID+".jsonl"), filepath.Join(f.second, "projects", project, f.session.ID+".jsonl")); err != nil {
		t.Fatal(err)
	}
	moved, err = f.c.RecoverRootMove(context.Background(), f.session.ID, placed, "work")
	if err != nil || !moved {
		t.Fatalf("a placed move recovered as moved=%v: %v", moved, err)
	}
	if !f.transcriptIn(t, f.second) || f.transcriptIn(t, primary) {
		t.Fatal("a placed move did not finish in the target")
	}
	if _, err := os.Stat(filepath.Join(f.second, "projects", project, f.session.ID, "subagents", "a.jsonl")); err != nil {
		t.Fatalf("the rest of a placed move did not follow: %v", err)
	}
	if _, err := os.Stat(archive); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the recovered move left its archive: %v", err)
	}
}

func gjsonKey(key string) string {
	escaped := ""
	for _, r := range key {
		if r == '.' || r == '*' || r == '?' {
			escaped += `\`
		}
		escaped += string(r)
	}
	return escaped
}

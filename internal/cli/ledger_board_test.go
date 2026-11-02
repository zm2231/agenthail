package cli

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/ledger"
	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/surface"
)

func TestLedgerBoardSaysWhatItCaptures(t *testing.T) {
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: "board-key"}); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	board := func(args ...string) string {
		t.Helper()
		out, err := captureStdout(t, func() error { return app.cmdLedger(args) })
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	if out := board(); !strings.Contains(out, "coordinator's prompts only") {
		t.Fatalf("unset project board %q", out)
	}
	if err := ls.Enable("board-key"); err != nil {
		t.Fatal(err)
	}
	if out := board(); !strings.Contains(out, "capturing since") || !strings.Contains(out, "not in the ledger") {
		t.Fatalf("enabled project board %q", out)
	}
	var enabled struct {
		Capture      string `json:"capture"`
		CaptureSince string `json:"captureSince"`
	}
	if err := json.Unmarshal([]byte(board("--json")), &enabled); err != nil || enabled.Capture != "enabled" || enabled.CaptureSince == "" {
		t.Fatalf("enabled board json %+v err %v", enabled, err)
	}
	if err := ls.Disable("board-key"); err != nil {
		t.Fatal(err)
	}
	if out := board(); !strings.Contains(out, "disabled") {
		t.Fatalf("disabled project board %q", out)
	}
}

func TestLedgerEnableAndDisableASession(t *testing.T) {
	elsewhere := surface.Session{ID: "elsewhere", Surface: surface.KindClaude, RuntimeKind: surface.RuntimeClaudeCLI, Cwd: t.TempDir()}
	app, store := cliFixture(t, &cliSurface{kind: surface.KindClaude, sessions: map[string]surface.Session{"elsewhere": elsewhere}})
	dir := t.TempDir()
	if err := store.AddProject(registry.ProjectRecord{ID: "proj", WorkspaceRoot: dir, Title: "proj", Key: "grant-key"}); err != nil {
		t.Fatal(err)
	}
	if err := store.RegisterSession(elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := store.SetAlias("helper", "elsewhere"); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	ls := ledger.NewStore(store.WriteDB(), store.ReadDB())
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"enable", "@helper"}) }); err != nil {
		t.Fatal(err)
	}
	grants, err := ls.SessionGrants("elsewhere")
	if err != nil || len(grants) != 1 || grants[0].ProjectKey != "grant-key" {
		t.Fatalf("grants %+v err %v", grants, err)
	}
	if scope, _ := ls.Scope("grant-key"); scope.State != ledger.LedgerUnset {
		t.Fatalf("enabling a session changed the project's own state: %+v", scope)
	}
	if _, err := captureStdout(t, func() error { return app.cmdLedger([]string{"disable", "@helper"}) }); err != nil {
		t.Fatal(err)
	}
	if grants, _ := ls.SessionGrants("elsewhere"); len(grants) != 0 {
		t.Fatalf("disable kept the grant: %+v", grants)
	}
}

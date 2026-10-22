package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/uiconf"
)

func uiFixture(t *testing.T) (*App, *uiconf.Store) {
	t.Helper()
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	app.UIDir = filepath.Join(t.TempDir(), "ui")
	store, err := uiconf.Open(app.UIDir)
	if err != nil {
		t.Fatal(err)
	}
	return app, store
}

func runUI(t *testing.T, app *App, args ...string) (string, error) {
	t.Helper()
	return captureStdout(t, func() error { return app.Run(append([]string{"ui"}, args...)) })
}

func TestUISetUnsetUndoAndLog(t *testing.T) {
	app, store := uiFixture(t)
	if out, err := runUI(t, app, "set", "theme = dark", "texture@phone = grain", "--message", "Night mode"); err != nil || !strings.Contains(out, "committed") {
		t.Fatalf("set: %q %v", out, err)
	}
	if _, err := runUI(t, app, "set", "glass.blur = 99"); err == nil || !strings.Contains(err.Error(), "glass.blur must be from 0 to 64") {
		t.Fatalf("out-of-range set err = %v", err)
	}
	out, err := runUI(t, app, "get", "--resolved", "--device", "phone", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var resolved struct {
		Values map[string]any `json:"values"`
	}
	if err := json.Unmarshal([]byte(out), &resolved); err != nil {
		t.Fatal(err)
	}
	if resolved.Values["theme"] != "dark" || resolved.Values["texture"] != "grain" {
		t.Fatalf("phone values = %v", resolved.Values)
	}
	if _, err := runUI(t, app, "unset", "texture@phone"); err != nil {
		t.Fatal(err)
	}
	if _, err := runUI(t, app, "undo"); err != nil {
		t.Fatal(err)
	}
	state, err := store.Current()
	if err != nil || !strings.Contains(state.Text, "texture@phone = grain") {
		t.Fatalf("after undo: %q %v", state.Text, err)
	}
	out, err = runUI(t, app, "log")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Undo Reset texture@phone", "Reset texture@phone", "Night mode", "Create ui.conf"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, uiconf.Person) {
		t.Fatalf("log should credit the person:\n%s", out)
	}
}

func TestUIWriteRefusesProblemsAndCheckReportsLines(t *testing.T) {
	app, store := uiFixture(t)
	bad := filepath.Join(t.TempDir(), "bad.conf")
	if err := os.WriteFile(bad, []byte("version = 1\ntheme = sepia\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runUI(t, app, "check", bad)
	if err != nil || !strings.Contains(out, "ui.conf:2: theme must be one of system, dark, light") {
		t.Fatalf("check: %q %v", out, err)
	}
	if _, err := runUI(t, app, "write", bad); err == nil {
		t.Fatal("write accepted a file with problems")
	}
	if state, _ := store.Current(); strings.Contains(state.Text, "sepia") {
		t.Fatal("refused write changed the file")
	}
	good := filepath.Join(t.TempDir(), "good.conf")
	if err := os.WriteFile(good, []byte("version = 1\ntheme = light\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runUI(t, app, "write", good); err != nil {
		t.Fatal(err)
	}
	if state, _ := store.Current(); state.Text != "version = 1\ntheme = light\n" {
		t.Fatalf("written text = %q", state.Text)
	}
}

func TestUISetImportsBackgroundImages(t *testing.T) {
	app, store := uiFixture(t)
	image := filepath.Join(t.TempDir(), "wall.png")
	if err := os.WriteFile(image, []byte("png"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runUI(t, app, "set", "background = image: "+image); err != nil {
		t.Fatal(err)
	}
	state, err := store.Current()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(state.Text, "background = image: backgrounds/") || strings.Contains(state.Text, image) {
		t.Fatalf("background line = %q", state.Text)
	}
}

func TestUIChangesByAnAgentCarryItsAlias(t *testing.T) {
	app, store := uiFixture(t)
	if err := app.Registry.RegisterSession(surface.Session{ID: "codex-ui", Surface: surface.KindCodex, RuntimeKind: surface.RuntimeCodexDesktop}); err != nil {
		t.Fatal(err)
	}
	if err := app.Registry.SetAlias("fixer", "codex-ui"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_THREAD_ID", "codex-ui")
	if _, err := runUI(t, app, "set", "theme = light"); err != nil {
		t.Fatal(err)
	}
	entries, err := store.Log(1)
	if err != nil || len(entries) != 1 || entries[0].Author != "@fixer" {
		t.Fatalf("author = %+v, %v", entries, err)
	}
}

func TestUIShowDefaultsNeedsNoRepository(t *testing.T) {
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindCodex})
	app.UIDir = filepath.Join(t.TempDir(), "never-created")
	out, err := runUI(t, app, "show-defaults", "--docs")
	if err != nil || !strings.Contains(out, "scrim.feather") || !strings.Contains(out, "aurora") {
		t.Fatalf("show-defaults: %v\n%s", err, out)
	}
	if _, err := os.Stat(app.UIDir); !os.IsNotExist(err) {
		t.Fatal("show-defaults created the ui repository")
	}
}

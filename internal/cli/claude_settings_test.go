package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface"
)

func compactSettingsFixture(t *testing.T) (string, string, string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	for _, name := range []string{"CLAUDE_CODE_AUTO_COMPACT_WINDOW", "DISABLE_AUTO_COMPACT", "DISABLE_COMPACT"} {
		t.Setenv(name, "")
		os.Unsetenv(name)
	}
	primary := filepath.Join(home, "elsewhere", "claude")
	t.Setenv("CLAUDE_CONFIG_DIR", primary)
	work := filepath.Join(home, ".claude-work")
	writeTestFile(t, filepath.Join(home, ".agenthail", "config.json"), `{"claudeConfigDirs":["`+work+`"]}`)
	return home, primary, work
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func compactSettings(t *testing.T, args ...string) []surface.ClaudeCompactSettings {
	t.Helper()
	app, _ := cliFixture(t, &cliSurface{kind: "claude"})
	output, err := captureStdout(t, func() error { return app.Run(append([]string{"claude", "settings", "compact", "--json"}, args...)) })
	if err != nil {
		t.Fatalf("%v: %v", args, err)
	}
	var report struct {
		Roots []surface.ClaudeCompactSettings `json:"roots"`
	}
	if err := json.Unmarshal([]byte(output), &report); err != nil {
		t.Fatalf("%v: output=%s", args, output)
	}
	return report.Roots
}

func TestCompactSettingsEditOnlyTheirKeysInTheRootsSettingsFile(t *testing.T) {
	_, _, work := compactSettingsFixture(t)
	path := filepath.Join(work, "settings.json")
	original := "{\n  \"model\": \"opus\",\n  \"hooks\": {\"Stop\": [{\"matcher\": \"*\"}]},\n  \"modelSettings\": {\"claude-opus-5-5\": {\"effortLevel\": \"high\"}}\n}\n"
	writeTestFile(t, path, original)
	unrelated := func() string {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return gjson.GetBytes(data, "model").Raw + gjson.GetBytes(data, "hooks").Raw + gjson.GetBytes(data, "modelSettings.claude-opus-5-5.effortLevel").Raw
	}
	before := unrelated()

	roots := compactSettings(t, "--root", work, "300000")
	if len(roots) != 1 || roots[0].Root != work || roots[0].AutoCompactWindow == nil || roots[0].AutoCompactWindow.Tokens != 300000 {
		t.Fatalf("roots=%+v", roots)
	}
	roots = compactSettings(t, "--root", "claude-work", "--model", "claude-opus-5-5", "auto", "--enabled", "off", "--idle", "off")
	if window := roots[0].ModelWindows["claude-opus-5-5"]; !window.Auto || roots[0].AutoCompactEnabled == nil || *roots[0].AutoCompactEnabled || roots[0].IdleCompaction == nil || *roots[0].IdleCompaction {
		t.Fatalf("roots=%+v", roots)
	}
	if after := unrelated(); after != before {
		t.Fatalf("unrelated settings changed:\n%s\n%s", before, after)
	}
	compactSettings(t, "--root", work, "unset")
	roots = compactSettings(t, "--root", work, "--model", "claude-opus-5-5", "unset", "--enabled", "on")
	data, _ := os.ReadFile(path)
	if roots[0].AutoCompactWindow != nil || len(roots[0].ModelWindows) != 0 || !*roots[0].AutoCompactEnabled || gjson.GetBytes(data, "autoCompactWindow").Exists() || unrelated() != before {
		t.Fatalf("roots=%+v file=%s", roots, data)
	}
}

func TestCompactSettingsAreReadPerRootWithTheConfiguredPrimaryRoot(t *testing.T) {
	home, primary, work := compactSettingsFixture(t)
	writeTestFile(t, filepath.Join(primary, "settings.json"), `{"autoCompactWindow":425000,"modelSettings":{"claude-opus-5-5":{"autoCompactWindow":250000}}}`)
	writeTestFile(t, filepath.Join(work, "settings.json"), `{"autoCompactWindow":"auto","idleCompaction":true}`)

	roots := compactSettings(t)
	if len(roots) != 2 || roots[0].Root != primary || roots[0].SettingsPath != filepath.Join(primary, "settings.json") || roots[1].Root != work {
		t.Fatalf("roots=%+v", roots)
	}
	if roots[0].AutoCompactWindow.Tokens != 425000 || roots[0].ModelWindows["claude-opus-5-5"].Tokens != 250000 || roots[0].IdleCompaction != nil {
		t.Fatalf("primary=%+v", roots[0])
	}
	if !roots[1].AutoCompactWindow.Auto || roots[1].ModelWindows != nil || roots[1].IdleCompaction == nil || !*roots[1].IdleCompaction {
		t.Fatalf("work=%+v", roots[1])
	}
	compactSettings(t, "300000")
	if roots := compactSettings(t); roots[0].AutoCompactWindow.Tokens != 300000 || !roots[1].AutoCompactWindow.Auto {
		t.Fatalf("a write without a root changed another root: %+v", roots)
	}
	if _, err := os.Stat(filepath.Join(home, ".claude")); !os.IsNotExist(err) {
		t.Fatalf("~/.claude was touched: %v", err)
	}
}

func TestCompactSettingsWithoutAFileOrWithValuesClaudeIgnoresReadAsSuch(t *testing.T) {
	_, primary, work := compactSettingsFixture(t)
	writeTestFile(t, filepath.Join(work, "settings.json"), `{"autoCompactWindow":50000,"modelSettings":{"claude-haiku-5-5":{"autoCompactWindow":"200k"}}}`)
	roots := compactSettings(t)
	if roots[0].Root != primary || roots[0].AutoCompactWindow != nil || roots[0].ModelWindows != nil || roots[0].AutoCompactEnabled != nil || roots[0].IdleCompaction != nil {
		t.Fatalf("a root without a settings file=%+v", roots[0])
	}
	if roots[1].AutoCompactWindow.Ignored != "50000" || roots[1].ModelWindows["claude-haiku-5-5"].Ignored != `"200k"` {
		t.Fatalf("ignored values=%+v", roots[1])
	}
	if _, err := os.Stat(filepath.Join(primary, "settings.json")); !os.IsNotExist(err) {
		t.Fatalf("a read created the settings file: %v", err)
	}
}

func TestCompactSettingsRefuseAWindowClaudeWouldIgnore(t *testing.T) {
	_, _, work := compactSettingsFixture(t)
	path := filepath.Join(work, "settings.json")
	writeTestFile(t, path, `{"autoCompactWindow":300000}`)
	for _, window := range []string{"99999", "1000001", "200k", "default"} {
		app, _ := cliFixture(t, &cliSurface{kind: "claude"})
		_, err := captureStdout(t, func() error {
			return app.Run([]string{"claude", "settings", "compact", "--root", work, window})
		})
		if err == nil || !strings.Contains(err.Error(), "100000 to 1000000") {
			t.Fatalf("%s: err=%v", window, err)
		}
	}
	if data, _ := os.ReadFile(path); string(data) != `{"autoCompactWindow":300000}` {
		t.Fatalf("a refused change wrote the file: %s", data)
	}
	for _, edge := range []string{"100000", "1000000"} {
		if roots := compactSettings(t, "--root", work, edge); roots[0].AutoCompactWindow == nil || roots[0].AutoCompactWindow.Tokens == 0 {
			t.Fatalf("%s: roots=%+v", edge, roots)
		}
	}
}

func TestCompactSettingsNameTheEnvironmentThatOverridesThem(t *testing.T) {
	compactSettingsFixture(t)
	t.Setenv("CLAUDE_CODE_AUTO_COMPACT_WINDOW", "360000")
	t.Setenv("DISABLE_AUTO_COMPACT", "1")
	roots := compactSettings(t)
	if roots[0].Overrides["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] != "360000" || roots[0].Overrides["DISABLE_AUTO_COMPACT"] != "1" || len(roots[0].Overrides) != 2 {
		t.Fatalf("overrides=%+v", roots[0].Overrides)
	}
}

func TestCompactSettingsKeepASymlinkedSettingsFile(t *testing.T) {
	home, primary, _ := compactSettingsFixture(t)
	target := filepath.Join(home, "dotfiles", "claude-settings.json")
	writeTestFile(t, target, `{"theme":"dark"}`)
	if err := os.MkdirAll(primary, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(primary, "settings.json")); err != nil {
		t.Fatal(err)
	}
	compactSettings(t, "300000")
	if info, err := os.Lstat(filepath.Join(primary, "settings.json")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Fatalf("the link was replaced: %v", err)
	}
	if data, _ := os.ReadFile(target); gjson.GetBytes(data, "autoCompactWindow").Int() != 300000 || gjson.GetBytes(data, "theme").String() != "dark" {
		t.Fatalf("target=%s", data)
	}
}

func TestCompactSettingsAddressEachModelByItsExactName(t *testing.T) {
	_, primary, _ := compactSettingsFixture(t)
	path := filepath.Join(primary, "settings.json")
	writeTestFile(t, path, `{"modelSettings":{"claude-opus-5-5":{"effortLevel":"high"}}}`)
	models := []string{"claude-opus-4.5", "us.anthropic.claude-opus-5-5-v1:0", `vendor\model`, "claude-*?", "42"}
	for _, model := range models {
		compactSettings(t, "--model", model, "200000")
	}
	roots := compactSettings(t)
	for _, model := range models {
		if roots[0].ModelWindows[model].Tokens != 200000 {
			t.Fatalf("%s: windows=%+v", model, roots[0].ModelWindows)
		}
	}
	for _, model := range models {
		compactSettings(t, "--model", model, "unset")
	}
	data, _ := os.ReadFile(path)
	if roots := compactSettings(t); len(roots[0].ModelWindows) != 0 || gjson.GetBytes(data, "modelSettings.claude-opus-5-5.effortLevel").String() != "high" || !gjson.GetBytes(data, "modelSettings").IsObject() {
		t.Fatalf("windows=%+v file=%s", roots[0].ModelWindows, data)
	}
	_, _, work := compactSettingsFixture(t)
	if roots := compactSettings(t, "--root", work, "--model", "42", "200000"); roots[0].ModelWindows["42"].Tokens != 200000 {
		data, _ := os.ReadFile(filepath.Join(work, "settings.json"))
		t.Fatalf("a numeric model name in a new file: %s", data)
	}
}

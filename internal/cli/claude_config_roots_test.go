package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestDoctorShowsEveryClaudeConfigRootAndABadConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	second := filepath.Join(home, "claude-work")
	if err := os.MkdirAll(second, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, filepath.Join(home, "claude-link")); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(home, ".agenthail", "config.json")
	if err := os.MkdirAll(filepath.Dir(config), 0700); err != nil {
		t.Fatal(err)
	}
	doctor := func(content string) (roots []string, configErr string) {
		t.Helper()
		if err := os.WriteFile(config, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
		app, _ := cliFixture(t, &cliSurface{kind: "claude"})
		output, _ := captureStdout(t, func() error { return app.Run([]string{"doctor", "--json"}) })
		var report struct {
			OK               bool     `json:"ok"`
			ClaudeConfigDirs []string `json:"claudeConfigDirs"`
			ClaudeConfigErr  string   `json:"claudeConfigError"`
		}
		if err := json.Unmarshal([]byte(output), &report); err != nil {
			t.Fatalf("output=%s err=%v", output, err)
		}
		if report.ClaudeConfigErr != "" && report.OK {
			t.Fatalf("doctor reported ok with a bad config: %s", output)
		}
		return report.ClaudeConfigDirs, report.ClaudeConfigErr
	}
	roots, configErr := doctor(`{"claudeConfigDirs":["` + second + `","` + filepath.Join(home, "claude-link") + `"]}`)
	if configErr != "" || len(roots) != 2 || roots[0] != filepath.Join(home, ".claude") || roots[1] != second {
		t.Fatalf("roots=%v err=%q", roots, configErr)
	}
	if roots, configErr := doctor(`{"claudeConfigDirs":["claude-work"]}`); configErr == "" || roots != nil {
		t.Fatalf("a relative root was accepted: roots=%v", roots)
	}
}

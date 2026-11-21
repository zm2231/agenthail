package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/tidwall/gjson"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

func TestTheDaemonRemovesAStopHookAnEarlierInstallWroteAndLeavesEveryOtherHook(t *testing.T) {
	d, _, _, _, _ := daemonFixture(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	settings := filepath.Join(home, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(settings), 0o700); err != nil {
		t.Fatal(err)
	}
	current := `exec '/opt/other/agenthail' hook session-context`
	retired := `exec '/usr/local/bin/agenthail' hook stop --surface claude --terminal`
	written := `{"model":"opus","hooks":{"UserPromptSubmit":[{"hooks":[{"type":"command","command":` + strconv.Quote(current) + `,"timeout":10}]}],"Stop":[{"hooks":[{"type":"command","command":"notify-done"},{"type":"command","command":` + strconv.Quote(retired) + `,"timeout":10}]}]}}`
	if err := os.WriteFile(settings, []byte(written), 0o640); err != nil {
		t.Fatal(err)
	}

	d.retireStrayHooks(t.Context(), home, codexsurface.CodexHome{Codex: filepath.Join(home, ".codex")})
	data, err := os.ReadFile(settings)
	if err != nil {
		t.Fatal(err)
	}
	if got := gjson.GetBytes(data, "hooks.Stop.#.hooks.#.command").Raw; got != `[["notify-done"]]` {
		t.Fatalf("the Stop hooks left are %s", got)
	}
	if gjson.GetBytes(data, "hooks.UserPromptSubmit.0.hooks.0.command").String() != current || gjson.GetBytes(data, "model").String() != "opus" || gjson.GetBytes(data, "hooks.PermissionRequest").Exists() {
		t.Fatalf("the daemon touched more than the retired hook: %s", data)
	}
	if info, err := os.Stat(settings); err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("settings mode %v err %v", info.Mode().Perm(), err)
	}

	untouched := `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify-done"}]}]}}`
	if err := os.WriteFile(settings, []byte(untouched), 0o640); err != nil {
		t.Fatal(err)
	}
	d.retireStrayHooks(t.Context(), home, codexsurface.CodexHome{Codex: filepath.Join(home, ".codex")})
	if data, _ := os.ReadFile(settings); string(data) != untouched {
		t.Fatalf("settings with no agenthail hook were rewritten: %s", data)
	}
}

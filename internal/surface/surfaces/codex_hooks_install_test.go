package surfaces

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

const testAgenthail = "/usr/local/bin/agenthail"

func isolatedCodexHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("HOME", home)
	return home
}

func codexContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func requireCodexState(t *testing.T, want string) {
	t.Helper()
	got, err := CodexContextState(codexContext(t), testAgenthail)
	if err != nil || got != want {
		t.Fatalf("state=%q err=%v, want %q", got, err, want)
	}
}

func TestHooksInstallCodexKeepsThePersonsConfigAndIsUndoneByUninstall(t *testing.T) {
	home := isolatedCodexHome(t)
	config := filepath.Join(home, "config.toml")
	original := "# my settings\nmodel = \"x\"\n\n[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"notify\" }] }]\n"
	memory := filepath.Join(home, "AGENTS.md")
	notes := "# My notes\n\nPrefer small commits.\n"
	for path, text := range map[string]string{config: original, memory: notes} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	requireCodexState(t, CodexHooksMissing)

	for range 2 {
		if _, err := InstallCodexContext(codexContext(t), testAgenthail); err != nil {
			t.Fatal(err)
		}
	}
	requireCodexState(t, CodexHooksInstalled)
	written, _ := os.ReadFile(config)
	if !strings.Contains(string(written), "# my settings") || !strings.Contains(string(written), `command = "notify"`) || strings.Count(string(written), "hook session-context --surface codex") != 2 || strings.Count(string(written), "trusted_hash") != 2 {
		t.Fatalf("config after install:\n%s", written)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("install wrote hooks.json beside config.toml hooks: %v", err)
	}
	instructions, _ := os.ReadFile(memory)
	if !strings.HasPrefix(string(instructions), notes) || strings.Count(string(instructions), codexMemoryBegin) != 1 || !strings.Contains(string(instructions), "agenthail help agent") {
		t.Fatalf("AGENTS.md after install:\n%s", instructions)
	}

	if _, err := UninstallCodexContext(codexContext(t)); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, CodexHooksMissing)
	written, _ = os.ReadFile(config)
	if strings.Contains(string(written), "session-context") || strings.Contains(string(written), "trusted_hash") || !strings.Contains(string(written), "# my settings") || !strings.Contains(string(written), `command = "notify"`) {
		t.Fatalf("config after uninstall:\n%s", written)
	}
	if instructions, _ := os.ReadFile(memory); string(instructions) != notes {
		t.Fatalf("AGENTS.md after uninstall:\n%q, want %q", instructions, notes)
	}
}

func TestHooksInstallCodexWritesIntoAHooksFileThePersonAlreadyUses(t *testing.T) {
	home := isolatedCodexHome(t)
	hooks := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"my-start"}]}]}}`), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexContext(codexContext(t), testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, CodexHooksInstalled)
	data, _ := os.ReadFile(hooks)
	if start := gjson.GetBytes(data, "hooks.SessionStart").Array(); len(start) != 2 || start[0].Get("hooks.0.command").String() != "my-start" || !ownsCodexContextHook(start[1].Get("hooks.0.command").String()) {
		t.Fatalf("hooks.json after install: %s", data)
	}
	if config, _ := os.ReadFile(filepath.Join(home, "config.toml")); strings.Contains(string(config), "session-context") {
		t.Fatalf("install put hooks in config.toml beside hooks.json:\n%s", config)
	}
	if info, err := os.Stat(hooks); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("hooks.json mode=%v err=%v", info.Mode().Perm(), err)
	}

	if _, err := UninstallCodexContext(codexContext(t)); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(hooks)
	if start := gjson.GetBytes(data, "hooks.SessionStart").Array(); len(start) != 1 || start[0].Get("hooks.0.command").String() != "my-start" || gjson.GetBytes(data, "hooks.UserPromptSubmit").Exists() {
		t.Fatalf("hooks.json after uninstall: %s", data)
	}
	requireCodexState(t, CodexHooksMissing)
}

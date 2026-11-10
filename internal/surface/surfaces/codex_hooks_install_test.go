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
	if out, err := exec.Command("codex", "--version").Output(); err != nil || !codexAtLeast(strings.TrimPrefix(strings.TrimSpace(string(out)), "codex-cli "), CodexStopHookVersion) {
		t.Skipf("codex %q is older than %s, which these tests install for", out, CodexStopHookVersion)
	}
	home := t.TempDir()
	t.Setenv("HOME", t.TempDir())
	return home
}

func codexContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

func requireCodexState(t *testing.T, home, want string) {
	t.Helper()
	got, err := CodexContextState(codexContext(t), CodexHome{Codex: home}, testAgenthail)
	if err != nil || got != want {
		t.Fatalf("state=%q err=%v, want %q", got, err, want)
	}
}

func TestHooksInstallCodexKeepsThePersonsConfigAndIsUndoneByUninstall(t *testing.T) {
	home := isolatedCodexHome(t)
	config := filepath.Join(home, "config.toml")
	original := "# my settings\nmodel = \"x\"\n\n[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"notify\" }] }]\nUserPromptSubmit = [{ hooks = [{ type = \"prompt\", prompt = \"Stay on task\" }] }]\n"
	memory := filepath.Join(home, "AGENTS.md")
	notes := "# My notes\n\nPrefer small commits.\n"
	for path, text := range map[string]string{config: original, memory: notes} {
		if err := os.WriteFile(path, []byte(text), 0600); err != nil {
			t.Fatal(err)
		}
	}
	requireCodexState(t, home, CodexHooksMissing)

	for range 2 {
		if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
			t.Fatal(err)
		}
	}
	requireCodexState(t, home, CodexHooksInstalled)
	written, _ := os.ReadFile(config)
	if !strings.Contains(string(written), "# my settings") || !strings.Contains(string(written), `command = "notify"`) || strings.Count(string(written), "hook session-context --surface codex") != 2 || strings.Count(string(written), "hook stop --surface codex") != 1 || strings.Count(string(written), "trusted_hash") != 3 {
		t.Fatalf("config after install:\n%s", written)
	}
	if _, err := os.Stat(filepath.Join(home, "hooks.json")); !os.IsNotExist(err) {
		t.Fatalf("install wrote hooks.json beside config.toml hooks: %v", err)
	}
	instructions, _ := os.ReadFile(memory)
	if !strings.HasPrefix(string(instructions), notes) || strings.Count(string(instructions), codexMemoryBegin) != 1 || !strings.Contains(string(instructions), "agenthail help agent") {
		t.Fatalf("AGENTS.md after install:\n%s", instructions)
	}

	if _, err := UninstallCodexContext(codexContext(t), CodexHome{Codex: home}); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksMissing)
	written, _ = os.ReadFile(config)
	if strings.Contains(string(written), "agenthail") || strings.Contains(string(written), "trusted_hash") || !strings.Contains(string(written), "# my settings") || !strings.Contains(string(written), `command = "notify"`) || !strings.Contains(string(written), `prompt = "Stay on task"`) {
		t.Fatalf("config after uninstall:\n%s", written)
	}
	if instructions, _ := os.ReadFile(memory); string(instructions) != notes {
		t.Fatalf("AGENTS.md after uninstall:\n%q, want %q", instructions, notes)
	}
	if _, err := os.Stat(filepath.Join(home, ".tmp")); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(home, ".tmp"))
		t.Fatalf("install left Codex's own work in CODEX_HOME/.tmp: %v", entries)
	}
}

func TestHooksInstallCodexWritesIntoAHooksFileThePersonAlreadyUses(t *testing.T) {
	home := isolatedCodexHome(t)
	hooks := filepath.Join(home, "hooks.json")
	mine := `[{"matcher":"startup","hooks":[{"type":"prompt","prompt":"Read the plan"},{"type":"command","command":"my-start"}]}]`
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"SessionStart":`+mine+`}}`), 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksInstalled)
	data, _ := os.ReadFile(hooks)
	if start := gjson.GetBytes(data, "hooks.SessionStart").Array(); len(start) != 2 || start[0].Raw != gjson.Get(mine, "0").Raw || !ownsCodexContextHook(start[1].Get("hooks.0.command").String()) {
		t.Fatalf("hooks.json after install: %s", data)
	}
	if !ownsCodexContextHook(gjson.GetBytes(data, "hooks.Stop.0.hooks.0.command").String()) {
		t.Fatalf("hooks.json after install has no Stop hook: %s", data)
	}
	if config, _ := os.ReadFile(filepath.Join(home, "config.toml")); strings.Contains(string(config), " --surface codex") {
		t.Fatalf("install put hooks in config.toml beside hooks.json:\n%s", config)
	}
	if info, err := os.Stat(hooks); err != nil || info.Mode().Perm() != 0640 {
		t.Fatalf("hooks.json mode=%v err=%v", info.Mode().Perm(), err)
	}
	if err := os.Remove(filepath.Join(home, "config.toml")); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksUntrusted)
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksInstalled)

	if _, err := UninstallCodexContext(codexContext(t), CodexHome{Codex: home}); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(hooks)
	if string(data) != `{"hooks":{"SessionStart":`+mine+`}}` {
		t.Fatalf("hooks.json after uninstall: %s", data)
	}
	requireCodexState(t, home, CodexHooksMissing)
}

func TestACodexTooOldToContinueABlockedTurnGetsNoStopHookUntilItIsUpdated(t *testing.T) {
	home := isolatedCodexHome(t)
	real, _ := exec.LookPath("codex")
	bin := t.TempDir()
	older := "#!/bin/sh\n'" + real + "' \"$@\" | sed -u 's#\"userAgent\":\"\\([^/\"]*\\)/[0-9.]*#\"userAgent\":\"\\1/0.162.0#'\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(older), 0700); err != nil {
		t.Fatal(err)
	}
	path := os.Getenv("PATH")
	t.Setenv("PATH", bin+":"+path)
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksNoNudge)
	config, _ := os.ReadFile(filepath.Join(home, "config.toml"))
	if strings.Contains(string(config), "hook stop") || strings.Count(string(config), "hook session-context --surface codex") != 2 {
		t.Fatalf("config after install on an older Codex:\n%s", config)
	}

	t.Setenv("PATH", path)
	requireCodexState(t, home, CodexHooksOutdated)
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksInstalled)

	t.Setenv("PATH", bin+":"+path)
	requireCodexState(t, home, CodexHooksOutdated)
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksNoNudge)
	if config, _ := os.ReadFile(filepath.Join(home, "config.toml")); strings.Contains(string(config), "hook stop") {
		t.Fatalf("install on a downgraded Codex left the Stop hook:\n%s", config)
	}
}

func TestHooksInstallCodexWithoutCodexWritesNothing(t *testing.T) {
	home := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	t.Setenv("HOME", home)
	t.Setenv("PATH", t.TempDir())
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: codexHome}, testAgenthail); err == nil {
		t.Fatal("install succeeded without a Codex to write its config")
	}
	if _, err := os.Stat(codexHome); !os.IsNotExist(err) {
		t.Fatalf("install left %s behind: %v", codexHome, err)
	}
	if state, err := CodexContextState(codexContext(t), CodexHome{Codex: codexHome}, testAgenthail); err != nil || state != "" {
		t.Fatalf("state=%q err=%v, want nothing to report", state, err)
	}
}

func TestHooksInstallCodexLeavesThePersonsAgentsFileByteForByte(t *testing.T) {
	for _, notes := range []string{"", "no final newline", "one final newline\n", "blank lines after\n\n\n"} {
		home := isolatedCodexHome(t)
		memory := filepath.Join(home, "AGENTS.md")
		if notes != "" {
			if err := os.WriteFile(memory, []byte(notes), 0600); err != nil {
				t.Fatal(err)
			}
		}
		for range 2 {
			if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
				t.Fatal(err)
			}
		}
		if installed, _ := os.ReadFile(memory); !strings.HasPrefix(string(installed), notes) || strings.Count(string(installed), codexMemoryBegin) != 1 {
			t.Fatalf("AGENTS.md after install over %q:\n%q", notes, installed)
		}
		if _, err := UninstallCodexContext(codexContext(t), CodexHome{Codex: home}); err != nil {
			t.Fatal(err)
		}
		after, err := os.ReadFile(memory)
		if notes == "" && !os.IsNotExist(err) {
			t.Fatalf("uninstall left AGENTS.md %q where there was none (err=%v)", after, err)
		}
		if notes != "" && string(after) != notes {
			t.Fatalf("AGENTS.md after uninstall %q, want %q", after, notes)
		}
	}
}

func TestCodexHooksStateReturnsByItsDeadlineWhenCodexWillNotExit(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	stubborn := "#!/bin/sh\nwhile read line; do\n  id=$(printf '%s' \"$line\" | sed -n 's/.*\"id\":\\([0-9]*\\).*/\\1/p')\n  [ -n \"$id\" ] && printf '{\"id\":%s,\"result\":{\"data\":[]}}\\n' \"$id\"\ndone\nexec sleep 60\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(stubborn), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	started := time.Now()
	if state, err := CodexContextState(ctx, CodexHome{Codex: home}, testAgenthail); err != nil || state != CodexHooksMissing {
		t.Fatalf("state=%q err=%v", state, err)
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("the state check took %s past its 2s deadline", elapsed)
	}
}

package codexsurface

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const testAgenthail = "/usr/local/bin/agenthail"

func isolatedCodexHome(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
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

func TestInstallAndUninstallRemoveAnAgenthailCodexHookNoLongerWritten(t *testing.T) {
	retired := `exec '` + testAgenthail + `' hook stop --surface codex`
	for _, file := range []string{"config.toml", "hooks.json"} {
		for _, action := range []string{"install", "uninstall"} {
			home := isolatedCodexHome(t)
			path := filepath.Join(home, file)
			written := "[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"notify\" }, { type = \"command\", command = " + strconv.Quote(retired) + ", timeout = 10 }] }]\n"
			if file == "hooks.json" {
				written = `{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify"},{"type":"command","command":` + strconv.Quote(retired) + `,"timeout":10}]}]}}`
			}
			if err := os.WriteFile(path, []byte(written), 0600); err != nil {
				t.Fatal(err)
			}
			requireCodexState(t, home, CodexHooksOutdated)
			var err error
			if action == "install" {
				_, err = InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail)
			} else {
				_, err = UninstallCodexContext(codexContext(t), CodexHome{Codex: home})
			}
			if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(path)
			if strings.Contains(string(data), "hook stop") || !strings.Contains(string(data), "notify") {
				t.Fatalf("%s after %s in %s:\n%s", file, action, home, data)
			}
			want := CodexHooksInstalled
			if action == "uninstall" {
				want = CodexHooksMissing
			}
			requireCodexState(t, home, want)
		}
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

func TestCodexAppServerThatExitsSaysWhyFromTheEndOfItsStderr(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := t.TempDir()
	failing := "#!/bin/sh\ni=0\nwhile [ $i -lt 200 ]; do echo \"early noise line $i\" >&2; i=$((i+1)); done\necho 'Error: failed to load config.toml: invalid key' >&2\nexit 3\n"
	if err := os.WriteFile(filepath.Join(bin, "codex"), []byte(failing), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+":/usr/bin:/bin")
	_, err := InstallCodexContext(codexContext(t), CodexHome{Codex: filepath.Join(home, ".codex")}, testAgenthail)
	if err == nil {
		t.Fatal("install succeeded against a Codex that exited")
	}
	message := err.Error()
	if !strings.Contains(message, "exit status 3") || !strings.HasSuffix(message, "Error: failed to load config.toml: invalid key") {
		t.Fatalf("error does not say why the app-server exited: %q", message)
	}
	if strings.Contains(message, "early noise line 0\n") || len(message) > codexStderrTailBytes+128 {
		t.Fatalf("error kept more than the end of stderr (%d bytes)", len(message))
	}
}

func TestRetiringCodexHooksRemovesOnlyAHookAgenthailNoLongerWritesAndKeepsTrust(t *testing.T) {
	retired := `exec '` + testAgenthail + `' hook stop --surface codex`
	home := isolatedCodexHome(t)
	hooks := filepath.Join(home, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"notify"}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(hooks)
	data, err := sjson.SetBytes(data, "hooks.Stop.0.hooks.-1", map[string]any{"type": "command", "command": retired, "timeout": 10})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(hooks, data, 0o600); err != nil {
		t.Fatal(err)
	}
	requireCodexState(t, home, CodexHooksOutdated)
	if changed, err := RetireCodexHooks(codexContext(t), CodexHome{Codex: home}); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	data, _ = os.ReadFile(hooks)
	if got := gjson.GetBytes(data, "hooks.Stop.#.hooks.#.command").Raw; got != `[["notify"]]` {
		t.Fatalf("hooks.json Stop after retiring: %s", got)
	}
	requireCodexState(t, home, CodexHooksInstalled)

	home = isolatedCodexHome(t)
	config := filepath.Join(home, "config.toml")
	if err := os.WriteFile(config, []byte("model = \"x\"\n\n[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"notify\" }, { type = \"command\", command = "+strconv.Quote(retired)+", timeout = 10 }] }]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := RetireCodexHooks(codexContext(t), CodexHome{Codex: home}); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	if written, _ := os.ReadFile(config); strings.Contains(string(written), "hook stop") || !strings.Contains(string(written), `command = "notify"`) || !strings.Contains(string(written), `model = "x"`) {
		t.Fatalf("config.toml after retiring:\n%s", written)
	}

	home = isolatedCodexHome(t)
	hooks = filepath.Join(home, "hooks.json")
	if err := os.WriteFile(hooks, []byte(`{"hooks":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexContext(codexContext(t), CodexHome{Codex: home}, testAgenthail); err != nil {
		t.Fatal(err)
	}
	installed, _ := os.ReadFile(hooks)
	config = filepath.Join(home, "config.toml")
	trusted, _ := os.ReadFile(config)
	if err := os.WriteFile(config, append([]byte("[hooks]\nStop = [{ hooks = [{ type = \"command\", command = \"notify\" }, { type = \"command\", command = "+strconv.Quote(retired)+", timeout = 10 }] }]\n\n"), trusted...), 0o600); err != nil {
		t.Fatal(err)
	}
	if changed, err := RetireCodexHooks(codexContext(t), CodexHome{Codex: home}); err != nil || !changed {
		t.Fatalf("a stray hook in config.toml beside hooks.json: changed=%v err=%v", changed, err)
	}
	if written, _ := os.ReadFile(config); strings.Contains(string(written), "hook stop") || !strings.Contains(string(written), `command = "notify"`) {
		t.Fatalf("config.toml beside hooks.json after retiring:\n%s", written)
	}
	if after, _ := os.ReadFile(hooks); string(after) != string(installed) {
		t.Fatalf("retiring changed hooks.json:\n%s\nwas:\n%s", after, installed)
	}
	requireCodexState(t, home, CodexHooksInstalled)

	if err := os.WriteFile(filepath.Join(home, "hooks.json"), []byte(`{"hooks":{"Stop":[{"hooks":[{"type":"command","command":"exec '/usr/local/bin/agenthail-wrapper' hook stop --surface codex"},{"type":"command","command":"echo \"/agenthail' hook stop --surface codex\""}]}]}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", t.TempDir())
	if changed, err := RetireCodexHooks(codexContext(t), CodexHome{Codex: home}); err != nil || changed {
		t.Fatalf("a Codex home with nothing to retire started Codex: changed=%v err=%v", changed, err)
	}
}

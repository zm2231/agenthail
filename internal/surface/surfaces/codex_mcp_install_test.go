package surfaces

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/tidwall/gjson"
)

const personsCodexConfig = `# Person's config. Keep this comment.
model = "gpt-x"   # trailing comment
approval_policy = "on-request"

[profiles.fast]  # profile comment
model = "small"
tools = { web_search = true }

# my notes server
[mcp_servers.notes]
command = "notes-mcp"
args = ["--fast"]
env = { NOTES_HOME = "/x", LEVEL = "2" }

[mcp_servers.other]
url = "http://127.0.0.1:9000/mcp"

[projects."/work/app"]
trust_level = "trusted"
`

func relayExecutableFile(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "agenthail")
	if err := os.WriteFile(path, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func requireRelayState(t *testing.T, home CodexHome, executable, want string) {
	t.Helper()
	if got, err := CodexRelayState(codexContext(t), home, executable); err != nil || got != want {
		t.Fatalf("relay state=%q err=%v, want %q", got, err, want)
	}
}

func withoutRelayTables(config string) string {
	start := strings.Index(config, "[mcp_servers."+CodexRelayServerName+"]")
	if start < 0 {
		return config
	}
	end := strings.Index(config[start:], "\n[projects.")
	return config[:start] + config[start+end+1:]
}

func TestInstallingTheCodexRelayWritesOnlyItsTablesAndUninstallRestoresTheBytes(t *testing.T) {
	home := CodexHome{Codex: isolatedCodexHome(t)}
	path := filepath.Join(home.Codex, "config.toml")
	if err := os.WriteFile(path, []byte(personsCodexConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := relayExecutableFile(t)
	requireRelayState(t, home, executable, CodexRelayMissing)

	if changed, err := InstallCodexRelay(codexContext(t), home, executable); err != nil || !changed {
		t.Fatalf("install: changed=%v err=%v", changed, err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(10 * time.Millisecond)
	if changed, err := InstallCodexRelay(codexContext(t), home, executable); err != nil || changed {
		t.Fatalf("install over a current entry: changed=%v err=%v", changed, err)
	}
	if after, err := os.Stat(path); err != nil || !os.SameFile(before, after) || !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("an install over a current entry wrote the config (err=%v)", err)
	}
	installed, _ := os.ReadFile(path)
	if rest := withoutRelayTables(string(installed)); rest != personsCodexConfig {
		t.Fatalf("install changed the person's config outside its own tables:\n%s", installed)
	}
	if !strings.Contains(string(installed), "command = "+fmt.Sprintf("%q", executable)) {
		t.Fatalf("install did not write the relay:\n%s", installed)
	}
	requireRelayState(t, home, executable, CodexRelayInstalled)
	requireRelayState(t, home, relayExecutableFile(t), CodexRelayOutdated)

	if changed, err := UninstallCodexRelay(codexContext(t), home); err != nil || !changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	if after, _ := os.ReadFile(path); string(after) != personsCodexConfig {
		t.Fatalf("uninstall left the config\n%s\nwant\n%s", after, personsCodexConfig)
	}

	if err := os.Remove(executable); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallCodexRelay(codexContext(t), home, executable); err != nil {
		t.Fatal(err)
	}
	requireRelayState(t, home, executable, CodexRelayDangling)
}

func TestTheCodexRelayInstallLeavesAServerItDidNotWrite(t *testing.T) {
	home := CodexHome{Codex: isolatedCodexHome(t)}
	path := filepath.Join(home.Codex, "config.toml")
	persons := personsCodexConfig + "\n[mcp_servers." + CodexRelayServerName + "]\ncommand = \"their-relay\"\n"
	if err := os.WriteFile(path, []byte(persons), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := relayExecutableFile(t)
	requireRelayState(t, home, executable, CodexRelayTaken)
	if _, err := InstallCodexRelay(codexContext(t), home, executable); err == nil {
		t.Fatal("install wrote over a server it did not add")
	}
	if changed, err := UninstallCodexRelay(codexContext(t), home); err != nil || changed {
		t.Fatalf("uninstall: changed=%v err=%v", changed, err)
	}
	if after, _ := os.ReadFile(path); string(after) != persons {
		t.Fatalf("the person's server changed:\n%s", after)
	}
	if changed, err := UninstallCodexRelay(codexContext(t), CodexHome{Codex: filepath.Join(t.TempDir(), "none")}); err != nil || changed {
		t.Fatalf("uninstall without a Codex home: changed=%v err=%v", changed, err)
	}
	requireRelayState(t, CodexHome{Codex: filepath.Join(t.TempDir(), "none")}, executable, "")
}

// codexStyleEdit writes the config the way Codex's own writer does: read it,
// change it, and rename a new file over it, with no lock.
func codexStyleEdit(path, add string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	next := path + ".next"
	if err := os.WriteFile(next, append(data, add...), 0o600); err != nil {
		return err
	}
	return os.Rename(next, path)
}

func TestAnInstallBuiltOnAConfigCodexHasSinceRewrittenIsBuiltAgain(t *testing.T) {
	home := CodexHome{Codex: isolatedCodexHome(t)}
	path := filepath.Join(home.Codex, "config.toml")
	if err := os.WriteFile(path, []byte(personsCodexConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	executable := relayExecutableFile(t)
	theirs := "\n[mcp_servers." + CodexRelayServerName + "]\ncommand = \"their-relay\"\n"
	builds := 0
	_, err := editCodexRelay(codexContext(t), home, func(user map[string]any) (any, bool, error) {
		builds++
		if builds == 1 {
			if err := codexStyleEdit(path, theirs); err != nil {
				t.Fatal(err)
			}
		}
		if codexRelayState(user, executable) == CodexRelayTaken {
			return nil, false, errors.New("taken")
		}
		return codexRelayEntry(executable), true, nil
	})
	if err == nil || builds != 2 {
		t.Fatalf("builds=%d err=%v; the install written over Codex's change was not refused and built again", builds, err)
	}
	if after, _ := os.ReadFile(path); string(after) != personsCodexConfig+theirs {
		t.Fatalf("the config Codex wrote changed:\n%s", after)
	}
}

func TestAThreadAgenthailStartsHasOnlyItsOwnServerWhileThePersonsThreadsGetTheRelay(t *testing.T) {
	realManagedCodex(t)
	home := CodexHomeFromEnv()
	if _, err := InstallCodexRelay(codexContext(t), home, relayExecutableFile(t)); err != nil {
		t.Fatal(err)
	}
	ctx := codexContext(t)
	client, err := dialManagedCodex(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	servers := func(threadID string) map[string]string {
		response, err := client.Request(ctx, "mcpServerStatus/list", map[string]any{"threadId": threadID, "detail": "toolsAndAuthOnly"}, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		found := map[string]string{}
		for _, server := range gjson.Get(codexJSON(response), "result.data").Array() {
			found[server.Get("name").String()] = server.Get("runtimeStatus").String()
		}
		return found
	}
	threadID := func(response map[string]any, err error) string {
		if err != nil {
			t.Fatal(err)
		}
		return gjson.Get(codexJSON(response), "result.thread.id").String()
	}

	c := &Codex{}
	c.SetMCPCredentials(&fakeCredentials{})
	ours := threadID(c.codexThreadRequest(ctx, client, codexThreadStart, nil, map[string]any{"cwd": t.TempDir()}, 30*time.Second))
	got := servers(ours)
	if _, ok := got[mcpServerName]; !ok || got[CodexRelayServerName] != "disabled" {
		t.Fatalf("a thread Agenthail started has servers %v, want its own %s and the relay disabled", got, mcpServerName)
	}

	persons := threadID(client.Request(ctx, codexThreadStart, map[string]any{"cwd": t.TempDir()}, 30*time.Second))
	got = servers(persons)
	if _, ok := got[CodexRelayServerName]; !ok || got[CodexRelayServerName] == "disabled" {
		t.Fatalf("the person's thread has servers %v, want the relay", got)
	}
	if _, ok := got[mcpServerName]; ok {
		t.Fatalf("the person's thread has Agenthail's own server: %v", got)
	}
}

package surfaces

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestEveryCodexThreadAgenthailStartsCarriesItsTrustedContextHooks(t *testing.T) {
	agenthailExecutable = func() string { return "/opt/it's/agenthail" }
	t.Cleanup(func() { agenthailExecutable = AgenthailExecutable })
	fake := startManagedCodex(t, func(method string, _ map[string]any) map[string]any {
		if method == "thread/start" {
			return map[string]any{"cwd": "/tmp/project", "thread": map[string]any{"id": "thread-new", "source": "appServer"}}
		}
		return nil
	})
	if _, err := isolatedManagedRuntime(t).PrepareManagedTerminalSession(context.Background(), "/tmp/project", ""); err != nil {
		t.Fatal(err)
	}
	starts := fake.Calls("thread/start")
	if len(starts) != 1 {
		t.Fatalf("methods=%v", fake.Methods())
	}
	data, _ := json.Marshal(starts[0]["config"])
	var config struct {
		Hooks map[string]json.RawMessage `json:"hooks"`
	}
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatalf("config=%s err=%v", data, err)
	}
	command := `exec '/opt/it'\''s/agenthail' hook session-context --surface codex`
	var state map[string]struct {
		TrustedHash string `json:"trusted_hash"`
	}
	if err := json.Unmarshal(config.Hooks["state"], &state); err != nil {
		t.Fatalf("config=%s err=%v", data, err)
	}
	for _, e := range codexContextEvents {
		var groups []struct {
			Hooks []struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				Timeout int    `json:"timeout"`
			} `json:"hooks"`
		}
		if err := json.Unmarshal(config.Hooks[e.event], &groups); err != nil || len(groups) != 1 || len(groups[0].Hooks) != 1 || groups[0].Hooks[0].Command != command || groups[0].Hooks[0].Type != "command" {
			t.Fatalf("%s hooks in %s err=%v", e.event, data, err)
		}
		if got := state["/<session-flags>/config.toml:"+e.key+":0:0"].TrustedHash; got != codexHookHash(e.key, command, codexContextHookTimeout) {
			t.Fatalf("%s trusted with %q in %s", e.event, got, data)
		}
	}
}

// Codex runs a hook only when its trusted hash is the hash Codex computes for
// it, so the hash Agenthail writes is checked against the installed Codex.
func TestTheInstalledCodexTrustsTheContextHooksAgenthailDeclares(t *testing.T) {
	codex, err := exec.LookPath("codex")
	if err != nil {
		t.Skip("codex is not installed")
	}
	home := t.TempDir()
	config := filepath.Join(home, "config.toml")
	command := CodexContextHookCommand("/usr/local/bin/agenthail")
	hooks, state := CodexContextHooks(command, config)
	var toml strings.Builder
	toml.WriteString("[hooks]\n")
	for _, e := range codexContextEvents {
		if _, ok := hooks[e.event]; !ok {
			t.Fatalf("no %s hook", e.event)
		}
		fmt.Fprintf(&toml, "%s = [{ hooks = [{ type = \"command\", command = %s, timeout = %d }] }]\n", e.event, strconv.Quote(command), codexContextHookTimeout)
	}
	for key, value := range state {
		fmt.Fprintf(&toml, "[hooks.state.%s]\ntrusted_hash = %s\n", strconv.Quote(key), strconv.Quote(value.(map[string]any)["trusted_hash"].(string)))
	}
	if err := os.WriteFile(config, []byte(toml.String()), 0600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	server := exec.CommandContext(ctx, codex, "app-server")
	server.Dir = home
	server.Env = append(os.Environ(), "CODEX_HOME="+home)
	stdin, err := server.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := server.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stdin.Close(); _ = server.Process.Kill(); _ = server.Wait() })
	for _, message := range []string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"agenthail-test","version":"0"}}}`,
		`{"jsonrpc":"2.0","method":"initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"hooks/list","params":{"cwds":[` + strconv.Quote(home) + `]}}`,
	} {
		if _, err := fmt.Fprintln(stdin, message); err != nil {
			t.Fatal(err)
		}
	}
	lines := bufio.NewScanner(stdout)
	lines.Buffer(make([]byte, 1<<20), 1<<24)
	for lines.Scan() {
		var response struct {
			ID     int `json:"id"`
			Result struct {
				Data []struct {
					Hooks []struct {
						EventName   string `json:"eventName"`
						Command     string `json:"command"`
						CurrentHash string `json:"currentHash"`
						TrustStatus string `json:"trustStatus"`
					} `json:"hooks"`
				} `json:"data"`
			} `json:"result"`
		}
		if json.Unmarshal(lines.Bytes(), &response) != nil || response.ID != 2 {
			continue
		}
		trusted := map[string]bool{}
		for _, data := range response.Result.Data {
			for _, hook := range data.Hooks {
				if hook.Command == command && hook.TrustStatus == "trusted" {
					trusted[hook.EventName] = true
				}
			}
		}
		if !trusted["sessionStart"] || !trusted["userPromptSubmit"] {
			t.Fatalf("Codex does not trust the context hooks Agenthail declares: %s", lines.Bytes())
		}
		return
	}
	t.Fatalf("Codex never answered hooks/list: %v", lines.Err())
}

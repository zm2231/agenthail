package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

func TestProxiesAddKeyAndRemoveStoreOnlyTheKeyReference(t *testing.T) {
	keys := claudeRootsHome(t)
	home, _ := os.UserHomeDir()
	app, _ := cliFixture(t, &cliSurface{kind: surface.KindClaude})
	run := func(args ...string) error {
		_, err := captureStdout(t, func() error { return app.Run(append([]string{"proxies"}, args...)) })
		return err
	}
	if err := run("add", "shared", "http://proxy.example:8317", "--key-env", "SHARED_KEY"); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"add", "shared", "http://other.example"},
		{"add", "bad name", "http://x"},
		{"add", "x", "ftp://x"},
		{"key", "nope", "--key-env", "X"},
		{"key", "shared"},
		{"key", "shared", "--key-env", "A", "--key-file", "/k"},
		{"rm", "nope"},
	} {
		if err := run(args...); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	if err := run("key", "shared", "--keychain-service", "agenthail-proxy", "--keychain-account", "shared"); err != nil {
		t.Fatal(err)
	}
	path := surface.AgenthailConfigPath(home)
	data, _ := os.ReadFile(path)
	var config struct {
		ClaudeConfigDirs []string          `json:"claudeConfigDirs"`
		Proxies          []json.RawMessage `json:"proxies"`
	}
	if err := json.Unmarshal(data, &config); err != nil || len(config.ClaudeConfigDirs) != 1 || config.ClaudeConfigDirs[0] != keys || len(config.Proxies) != 1 {
		t.Fatalf("the rest of the config is kept: %s", data)
	}
	var proxy struct {
		Management map[string]any `json:"management"`
	}
	json.Unmarshal(config.Proxies[0], &proxy)
	if keychain, _ := proxy.Management["keychain"].(map[string]any); len(proxy.Management) != 1 || keychain["service"] != "agenthail-proxy" || keychain["account"] != "shared" {
		t.Fatalf("the key reference is replaced: %s", config.Proxies[0])
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Fatalf("mode=%v", info.Mode())
	}
	for _, args := range [][]string{
		{"key", "shared", "--key-command"},
		{"key", "shared", "--key-command", "--"},
		{"key", "shared", "--key-command", "--", "no-such-key-command-here"},
		{"key", "shared", "--key-env", "A", "--key-command", "--", "sh"},
	} {
		if err := run(args...); err == nil {
			t.Fatalf("%v was accepted", args)
		}
	}
	if err := run("key", "shared", "--key-command", "--", "sh", "-c", "--key-env"); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(path)
	json.Unmarshal(data, &config)
	proxy.Management = nil
	json.Unmarshal(config.Proxies[0], &proxy)
	if command, _ := proxy.Management["command"].([]any); len(proxy.Management) != 1 || len(command) != 3 || !strings.HasPrefix(command[0].(string), "/") || command[2] != "--key-env" {
		t.Fatalf("the command is stored as argv with an absolute path: %s", config.Proxies[0])
	}
	if err := run("rm", "shared"); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(path); strings.Contains(string(data), "proxies") {
		t.Fatalf("removed: %s", data)
	}
}

func TestUsagePrintsAProxyGroupWithEveryAccountAndItsSessions(t *testing.T) {
	now := time.Date(2026, 10, 26, 12, 0, 0, 0, time.UTC)
	var sets []usageSetView
	if err := json.Unmarshal([]byte(`[
	  {"host":"laptop","hostId":"h1","local":true,"provider":"claude","name":"work","login":"proxy","root":"/w","proxy":{"name":"shared","id":"http://proxy.example:8317"},"meters":[]},
	  {"host":"server","hostId":"h2","provider":"proxy","name":"shared","proxy":{"name":"shared","id":"http://proxy.example:8317","url":"http://proxy.example:8317"},"meters":[],"accounts":[
	    {"id":"a","provider":"claude","label":"dev@example.com","status":"active","asOf":"2026-10-26T11:48:00Z","meters":[{"kind":"session","label":"Claude","percent":18,"ceiling":60},{"kind":"weekly","label":"Claude week","percent":41,"ceiling":60}]},
	    {"id":"b","provider":"claude","label":"team@example.com","status":"cooling","nextRetry":"2026-10-26T14:20:00Z","meters":[]}],
	   "problem":"Every shared Claude account is over its ceiling; the first frees up in 2h 20m."}
	]`), &sets); err != nil {
		t.Fatal(err)
	}
	sessions := []usageSnapshotSession{{ID: "s1", Surface: "claude", Name: "Fix it", Status: "busy", Host: &struct {
		ID string `json:"id"`
	}{ID: "h1"}, ClaudeConfigRoot: &struct {
		Root string `json:"root"`
	}{Root: "/w"}}}
	for i := range sets {
		sets[i].Sessions = usageSessions(sets[i], sessions)
	}
	sets[1].Sessions = proxySessions(sets[1], sets)
	var out bytes.Buffer
	writeUsage(&out, sets, now)
	want := `laptop
  work via the shared proxy
    sessions: Fix it (working)
server
  shared (proxy)
    dev@example.com          claude  session 18% (cap 60%)  week 41% (cap 60%)  read 12m ago
    team@example.com         claude, cooling for 2h 20m
    Every shared Claude account is over its ceiling; the first frees up in 2h 20m.
    sessions: Fix it (working)
`
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

func TestProxiesListsAProxyWithTheRootsThatUseIt(t *testing.T) {
	var sets []usageSetView
	json.Unmarshal([]byte(`[
	  {"host":"laptop","hostId":"h1","local":true,"provider":"claude","name":"work","login":"proxy","proxy":{"name":"shared","id":"p"}},
	  {"host":"laptop","hostId":"h1","local":true,"provider":"proxy","name":"shared","proxy":{"name":"shared","id":"p","url":"http://proxy.example:8317"},"problem":"No management key for the shared proxy on this host.","problemDetail":"Add one with agenthail proxies key shared --key-file <path>."}
	]`), &sets)
	var out bytes.Buffer
	writeProxies(&out, proxyViews(sets), false, time.Now())
	want := "shared (proxy)  http://proxy.example:8317\n  No management key for the shared proxy on this host.\n  Add one with agenthail proxies key shared --key-file <path>.\n  roots: work on laptop\n"
	if out.String() != want {
		t.Fatalf("output:\n%s\nwant:\n%s", out.String(), want)
	}
}

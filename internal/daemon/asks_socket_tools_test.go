package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/registry"
)

func TestASpawnerWhoseOwnCodexStartsTheRelayIsToldItsWorkersAskInToolForm(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	realHome := t.TempDir()
	codexHome := filepath.Join(home, ".codex")
	if err := os.Symlink(realHome, codexHome); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_HOME", codexHome)
	d, r, fake, from, to := daemonFixture(t)
	if err := r.RecordSessionOrigin(registry.OriginOf(to.ID, from.ID)); err != nil {
		t.Fatal(err)
	}
	f := askFixture{d: d, r: r, fake: fake, worker: mcpSession(t, mcpServer(t, d), issueMCPToken(t, d, to)), from: from, to: to}
	tellsOf := func() string {
		return strings.Join(append(f.fake.sent, f.told(t, from.ID)...), "\n")
	}

	f.raise(t, f.worker, deployAsk)
	if told := tellsOf(); !strings.Contains(told, "agenthail ask answer ") || strings.Contains(told, "agenthail_answer") {
		t.Fatalf("a spawner without the tools was told %q", told)
	}

	command := filepath.Join(t.TempDir(), "agenthail")
	if err := os.WriteFile(command, nil, 0o700); err != nil {
		t.Fatal(err)
	}
	relay := "[mcp_servers.agenthail_relay]\ncommand = \"" + command + "\"\nargs = [\"mcp\", \"stdio\"]\n\n[mcp_servers.agenthail_relay.env]\nAGENTHAIL_MCP_INSTALLED_BY = \"agenthail mcp install\"\n"
	if err := os.WriteFile(filepath.Join(codexHome, "config.toml"), []byte(relay), 0o600); err != nil {
		t.Fatal(err)
	}
	runsFrom := func(rollout string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(rollout), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(rollout, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		from.Transcript = rollout
		if err := r.RegisterSession(from); err != nil {
			t.Fatal(err)
		}
		f.fake.sent = nil
		f.raise(t, f.worker, deployAsk)
	}
	runsFrom(filepath.Join(t.TempDir(), "sessions", "rollout-from.jsonl"))
	if told := tellsOf(); strings.Contains(told, "agenthail_answer") {
		t.Fatalf("a spawner whose thread runs from another Codex home was told %q", told)
	}
	for _, rollout := range []string{filepath.Join(codexHome, "sessions", "rollout-from.jsonl"), filepath.Join(realHome, "sessions", "rollout-from.jsonl")} {
		runsFrom(rollout)
		if told := tellsOf(); !strings.Contains(told[strings.LastIndex(told, "[agenthail]"):], `agenthail_answer {"requestId":"`) {
			t.Fatalf("a spawner whose own Codex starts the relay, its rollout at %s, was told %q", rollout, told)
		}
	}
}

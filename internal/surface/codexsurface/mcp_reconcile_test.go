package codexsurface

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReconcilingTheCodexRelayNeverRewritesAnEntryThePersonEdited(t *testing.T) {
	home := CodexHome{Codex: isolatedCodexHome(t)}
	path := filepath.Join(home.Codex, "config.toml")
	if err := os.WriteFile(path, []byte(personsCodexConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	old, now := relayExecutableFile(t), relayExecutableFile(t)
	reconcile := func(executable string, off bool) bool {
		t.Helper()
		changed, err := ReconcileCodexRelay(codexContext(t), home, executable, off)
		if err != nil {
			t.Fatal(err)
		}
		return changed
	}
	if !reconcile(old, false) {
		t.Fatal("a missing relay was not added")
	}
	requireRelayState(t, home, old, CodexRelayInstalled)
	if reconcile(old, false) {
		t.Fatal("a current relay was written again")
	}
	if !reconcile(now, false) {
		t.Fatal("install's own entry did not follow the binary")
	}
	requireRelayState(t, home, now, CodexRelayInstalled)

	installed, _ := os.ReadFile(path)
	edited := strings.Replace(string(installed), `"stdio"]`, `"stdio", "--verbose"]`, 1)
	if edited == string(installed) {
		t.Fatalf("no args line to edit in:\n%s", installed)
	}
	if err := os.WriteFile(path, []byte(edited), 0o600); err != nil {
		t.Fatal(err)
	}
	if reconcile(old, false) || reconcile(old, true) {
		t.Fatal("reconcile rewrote an entry the person edited")
	}
	if after, _ := os.ReadFile(path); string(after) != edited {
		t.Fatalf("the edited entry changed:\n%s", after)
	}

	if err := os.WriteFile(path, installed, 0o600); err != nil {
		t.Fatal(err)
	}
	if !reconcile(now, true) {
		t.Fatal("off did not remove install's own entry")
	}
	if after, _ := os.ReadFile(path); string(after) != personsCodexConfig {
		t.Fatalf("off left the config\n%s", after)
	}
}

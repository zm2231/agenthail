package surfaces

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCodexDialsTheDefaultDesktopPortOnlyFromTheAccountsOwnCodexHome(t *testing.T) {
	restoreRuns, restoreAccount := codexDesktopRunsHere, codexAccountHome
	t.Cleanup(func() { codexDesktopRunsHere, codexAccountHome = restoreRuns, restoreAccount })
	codexDesktopRunsHere = true

	root := t.TempDir()
	account := filepath.Join(root, "account")
	if err := os.MkdirAll(filepath.Join(account, ".codex"), 0o755); err != nil {
		t.Fatal(err)
	}
	scratch := filepath.Join(root, "scratch")
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "linked-codex")
	if err := os.Symlink(filepath.Join(account, ".codex"), link); err != nil {
		t.Fatal(err)
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	codexAccountHome = func() string { return account }

	for _, test := range []struct {
		name      string
		home      string
		codexHome string
		port      string
		want      string
	}{
		{name: "the account's home with no CODEX_HOME", home: account, want: "ws://127.0.0.1:9231"},
		{name: "a scratch HOME with no CODEX_HOME", home: scratch, want: ""},
		{name: "a CODEX_HOME of its own", home: account, codexHome: filepath.Join(scratch, ".codex"), want: ""},
		{name: "CODEX_HOME set to the account's own, written uncleanly", home: scratch, codexHome: account + "/./.codex/", want: "ws://127.0.0.1:9231"},
		{name: "CODEX_HOME through a symlink to the account's own", home: scratch, codexHome: link, want: "ws://127.0.0.1:9231"},
		{name: "CODEX_HOME at the account's own by its resolved path", home: scratch, codexHome: filepath.Join(resolvedRoot, "account", ".codex"), want: "ws://127.0.0.1:9231"},
		{name: "an explicit port from a scratch HOME", home: scratch, port: "4567", want: "ws://127.0.0.1:4567"},
		{name: "an explicit port from a CODEX_HOME of its own", home: account, codexHome: filepath.Join(scratch, ".codex"), port: "4567", want: "ws://127.0.0.1:4567"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("HOME", test.home)
			t.Setenv("CODEX_HOME", test.codexHome)
			if got := NewCodex(test.port).desktopURL; got != test.want {
				t.Fatalf("desktop endpoint %q, want %q", got, test.want)
			}
		})
	}

	t.Run("an account home that cannot be read", func(t *testing.T) {
		codexAccountHome = func() string { return "" }
		t.Cleanup(func() { codexAccountHome = func() string { return account } })
		t.Setenv("HOME", account)
		t.Setenv("CODEX_HOME", "")
		if got := NewCodex("").desktopURL; got != "" {
			t.Fatalf("desktop endpoint %q without a known account home", got)
		}
	})

	t.Run("the account's own home before its .codex exists", func(t *testing.T) {
		fresh := filepath.Join(root, "fresh")
		if err := os.MkdirAll(fresh, 0o755); err != nil {
			t.Fatal(err)
		}
		codexAccountHome = func() string { return fresh }
		t.Cleanup(func() { codexAccountHome = func() string { return account } })
		t.Setenv("HOME", scratch)
		t.Setenv("CODEX_HOME", filepath.Join(resolvedRoot, "fresh", ".codex"))
		if got := NewCodex("").desktopURL; got != "ws://127.0.0.1:9231" {
			t.Fatalf("desktop endpoint %q", got)
		}
	})
}

func TestACodexHomeOfItsOwnNamesTheDesktopOptIn(t *testing.T) {
	restoreRuns, restoreAccount := codexDesktopRunsHere, codexAccountHome
	t.Cleanup(func() { codexDesktopRunsHere, codexAccountHome = restoreRuns, restoreAccount })
	codexDesktopRunsHere = true
	account := t.TempDir()
	codexAccountHome = func() string { return account }
	t.Setenv("HOME", t.TempDir())
	t.Setenv("CODEX_HOME", "")

	codex := NewCodex("")
	err := codex.DesktopReady(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not used from this Codex home") || !strings.Contains(err.Error(), "AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT") {
		t.Fatalf("Desktop from a Codex home of its own: %v", err)
	}
	if problem, remediation := codex.desktopProblem(); problem != "desktop-unreachable" || remediation != codexDesktopOptIn {
		t.Fatalf("problem %q remediation %q", problem, remediation)
	}
}

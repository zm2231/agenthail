package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

func TestDaemonLogPathMatchesInstallChannel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if got := daemonLogPathForExecutable("/opt/homebrew/Cellar/agenthail/0.2.8/libexec/agenthail"); got != "/opt/homebrew/var/log/agenthail.log" {
		t.Fatalf("Homebrew log path=%q", got)
	}
	if got := daemonLogPathForExecutable("/usr/local/Cellar/agenthail/0.2.8/libexec/agenthail"); got != "/usr/local/var/log/agenthail.log" {
		t.Fatalf("Intel Homebrew log path=%q", got)
	}
	if got := daemonLogPathForExecutable("/usr/local/bin/agenthail"); got != daemon.LogFilePath() {
		t.Fatalf("package log path=%q", got)
	}
}

func TestDashboardConfigCommandSetsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	output, err := captureStdout(t, func() error {
		return app.cmdDashboard([]string{"config", "--codex-recent-hours", "7"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "7h") {
		t.Fatalf("output=%q", output)
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CodexRecentHours != 7 {
		t.Fatalf("Codex recent hours=%d, want 7", config.CodexRecentHours)
	}
}

func TestDashboardRemoteStatusDispatchesMultiwordCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "tailscale")
	body := "#!/bin/sh\nif [ \"$1\" = \"status\" ]; then echo '{\"BackendState\":\"Running\",\"Self\":{\"DNSName\":\"agent.tailnet.ts.net.\",\"Online\":true}}'; else echo '{}'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	output, err := captureStdout(t, func() error {
		return app.cmdDashboard([]string{"remote", "status", "--tailscale", script})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "remote dashboard access: off") || strings.Contains(output, "opened dashboard") {
		t.Fatalf("output=%q", output)
	}
}

func TestDashboardRemoteOffClearsDesiredStateWithoutRoute(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "tailscale")
	body := "#!/bin/sh\nif [ \"$1\" = \"status\" ]; then echo '{\"BackendState\":\"Running\",\"Self\":{\"DNSName\":\"agent.tailnet.ts.net.\",\"Online\":true}}'; else echo '{}'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	config := daemon.DashboardConfig{Enabled: true, RemoteAccess: daemon.RemoteAccessConfig{Enabled: true}}
	if err := daemon.SaveDashboardConfig(config); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	if _, err := captureStdout(t, func() error {
		return app.cmdDashboard([]string{"remote", "off", "--tailscale", script})
	}); err != nil {
		t.Fatal(err)
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.RemoteAccess.Enabled {
		t.Fatal("remote access remained enabled after idempotent off")
	}
}

func TestDashboardConfigCommandRejectsInvalidCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	if err := app.cmdDashboard([]string{"config", "--codex-recent-hours", "0"}); err == nil {
		t.Fatal("zero-hour Codex window accepted")
	}
}

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

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

func TestDashboardShareStatusDispatchesMultiwordCommand(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "tailscale")
	body := "#!/bin/sh\nif [ \"$1\" = \"status\" ]; then echo '{\"BackendState\":\"Running\",\"Self\":{\"DNSName\":\"agent.tailnet.ts.net.\",\"Online\":true}}'; else echo '{}'; fi\n"
	if err := os.WriteFile(script, []byte(body), 0700); err != nil {
		t.Fatal(err)
	}
	app := &App{}
	output, err := captureStdout(t, func() error {
		return app.cmdDashboard([]string{"share", "status", "--tailscale", script})
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output, "dashboard share: off") || strings.Contains(output, "opened dashboard") {
		t.Fatalf("output=%q", output)
	}
}

func TestDashboardConfigCommandRejectsInvalidCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	if err := app.cmdDashboard([]string{"config", "--codex-recent-hours", "0"}); err == nil {
		t.Fatal("zero-hour Codex window accepted")
	}
}

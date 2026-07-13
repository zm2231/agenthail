package cli

import (
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

func TestDashboardConfigCommandRejectsInvalidCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	if err := app.cmdDashboard([]string{"config", "--codex-recent-hours", "0"}); err == nil {
		t.Fatal("zero-hour Codex window accepted")
	}
}

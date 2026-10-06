package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/zm2231/agenthail/internal/daemon"
)

func TestDashboardConfigCommandSetsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	app := &App{}
	output, err := captureStdout(t, func() error {
		return app.Run([]string{"dashboard", "config", "--codex-recent-hours", "7"})
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
		return app.Run([]string{"dashboard", "remote", "status", "--json", "--tailscale", script})
	})
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Enabled *bool  `json:"enabled"`
		DNSName string `json:"dnsName"`
	}
	if err := json.Unmarshal([]byte(output), &status); err != nil || status.Enabled == nil || *status.Enabled || status.DNSName != "agent.tailnet.ts.net" {
		t.Fatalf("output=%q err=%v", output, err)
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
		return app.Run([]string{"dashboard", "remote", "off", "--tailscale", script})
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
	if err := app.Run([]string{"dashboard", "config", "--codex-recent-hours", "0"}); err == nil {
		t.Fatal("zero-hour Codex window accepted")
	}
}

func TestDaemonServicePlistRunsAtStandardProcessType(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd plists are validated with macOS plutil")
	}
	path := filepath.Join(t.TempDir(), "service.plist")
	plist := daemonServicePlist("/opt/agenthail & tools/agenthail", "    <key>PATH</key><string>/usr/bin</string>\n", "/tmp/agenthail.log")
	if err := os.WriteFile(path, []byte(plist), 0o600); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{
		"ProcessType":               "Standard",
		"ProgramArguments.0":        "/opt/agenthail & tools/agenthail",
		"EnvironmentVariables.PATH": "/usr/bin",
	} {
		output, err := exec.Command("plutil", "-extract", key, "raw", "-o", "-", path).CombinedOutput()
		if err != nil {
			t.Fatalf("plutil -extract %s: %v: %s", key, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != want {
			t.Fatalf("%s=%q, want %q", key, got, want)
		}
	}
}

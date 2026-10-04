package daemon

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDashboardConfigDefaultsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config, err := LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CodexRecentHours != defaultCodexRecentHours {
		t.Fatalf("Codex recent hours=%d, want %d", config.CodexRecentHours, defaultCodexRecentHours)
	}
	if config.BusyDelivery != "queue" {
		t.Fatalf("busy delivery=%q, want queue", config.BusyDelivery)
	}
	if config.RemoteAccess.Provider != "tailscale" || config.RemoteAccess.Port != defaultRemoteAccessPort {
		t.Fatalf("remote access=%+v", config.RemoteAccess)
	}
}

func TestDashboardConfigPersistsBusyDelivery(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := SaveDashboardConfig(DashboardConfig{BusyDelivery: "steer"}); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDashboardConfig()
	if err != nil || loaded.BusyDelivery != "steer" {
		t.Fatalf("config=%+v err=%v", loaded, err)
	}
}

func TestDashboardConfigRejectsInvalidValues(t *testing.T) {
	for name, config := range map[string]DashboardConfig{
		"busy delivery": {BusyDelivery: "drop"},
		"codex recency": {CodexRecentHours: 169},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := SaveDashboardConfig(config); err == nil {
				t.Fatalf("invalid config accepted: %+v", config)
			}
		})
	}
}

func TestDashboardConfigPersistsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := DashboardConfig{Enabled: true, Listen: defaultDashboardListen, CodexRecentHours: 9}
	if err := SaveDashboardConfig(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CodexRecentHours != 9 {
		t.Fatalf("Codex recent hours=%d, want 9", loaded.CodexRecentHours)
	}
	info, err := os.Stat(DashboardConfigPath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions=%v, want 0600", info.Mode().Perm())
	}
}

func TestDashboardConfigMigratesMissingCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(DashboardConfigPath()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(DashboardConfigPath(), []byte(`{"enabled":true,"listen":"127.0.0.1:7412"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := LoadDashboardConfig()
	if err != nil {
		t.Fatal(err)
	}
	if config.CodexRecentHours != defaultCodexRecentHours {
		t.Fatalf("Codex recent hours=%d, want %d", config.CodexRecentHours, defaultCodexRecentHours)
	}
}

func TestDashboardListenIsLoopbackOnly(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for _, listen := range []string{"127.0.0.1:7412", "[::1]:7412", "localhost:7412"} {
		if err := SaveDashboardConfig(DashboardConfig{Listen: listen}); err != nil {
			t.Fatalf("%s: %v", listen, err)
		}
	}
	if err := SaveDashboardConfig(DashboardConfig{Listen: "0.0.0.0:7412"}); err == nil {
		t.Fatal("non-loopback listener was saved")
	}
	if err := os.WriteFile(DashboardConfigPath(), []byte(`{"enabled":true,"listen":"0.0.0.0:7412"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadDashboardConfig(); err == nil {
		t.Fatal("hand-edited non-loopback listener was loaded")
	}
}

package dashconfig

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigDefaultsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if config.CodexRecentHours != defaultCodexRecentHours {
		t.Fatalf("Codex recent hours=%d, want %d", config.CodexRecentHours, defaultCodexRecentHours)
	}
	if config.BusyDelivery != "queue" {
		t.Fatalf("busy delivery=%q, want queue", config.BusyDelivery)
	}
	if config.RemoteAccess.Provider != "tailscale" || config.RemoteAccess.Port != DefaultRemoteAccessPort {
		t.Fatalf("remote access=%+v", config.RemoteAccess)
	}
}

func TestConfigRejectsInvalidValues(t *testing.T) {
	for name, config := range map[string]Config{
		"busy delivery": {BusyDelivery: "drop"},
		"codex recency": {CodexRecentHours: 169},
	} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			if err := Save(config); err == nil {
				t.Fatalf("invalid config accepted: %+v", config)
			}
		})
	}
}

func TestConfigPersistsCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config := Config{Enabled: true, Listen: DefaultListen, CodexRecentHours: 9}
	if err := Save(config); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if loaded.CodexRecentHours != 9 {
		t.Fatalf("Codex recent hours=%d, want 9", loaded.CodexRecentHours)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions=%v, want 0600", info.Mode().Perm())
	}
}

func TestConfigMigratesMissingCodexRecency(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if err := os.MkdirAll(filepath.Dir(Path()), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(Path(), []byte(`{"enabled":true,"listen":"127.0.0.1:7412"}`), 0600); err != nil {
		t.Fatal(err)
	}
	config, err := Load()
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
		if err := Save(Config{Listen: listen}); err != nil {
			t.Fatalf("%s: %v", listen, err)
		}
	}
	if err := Save(Config{Listen: "0.0.0.0:7412"}); err == nil {
		t.Fatal("non-loopback listener was saved")
	}
	if err := os.WriteFile(Path(), []byte(`{"enabled":true,"listen":"0.0.0.0:7412"}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Fatal("hand-edited non-loopback listener was loaded")
	}
}

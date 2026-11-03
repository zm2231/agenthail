package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/registry"
	"github.com/zm2231/agenthail/internal/registry/registrytest"
)

func TestTheDaemonRecordsItsClaudeRootBeforeItServes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, "persist", "claude"))
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d := newTestDaemon(t, store, nil)
	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan error, 1)
	go func() { stopped <- d.Run(ctx) }()
	path := filepath.Join(home, ".agenthail", "daemon-claude-root")
	deadline := time.Now().Add(5 * time.Second)
	for {
		if data, err := os.ReadFile(path); err == nil && strings.TrimSpace(string(data)) == filepath.Join(home, "persist", "claude") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not record its Claude root at %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	<-stopped
}

func TestTheDaemonRefusesToStartWhenItCannotRecordItsClaudeRoot(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	if err := os.WriteFile(filepath.Join(home, ".agenthail"), []byte("not a folder"), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := registry.Open(registrytest.Path(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	d := newTestDaemon(t, store, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := d.Run(ctx); err == nil || !strings.Contains(err.Error(), "Claude config root") {
		t.Fatalf("err=%v, want the daemon to refuse to start", err)
	}
}

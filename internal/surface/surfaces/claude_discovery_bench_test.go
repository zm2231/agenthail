package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zm2231/agenthail/internal/signin"
)

// discoveryFixture spreads the same live sessions and transcripts over the
// given number of Claude config roots.
func discoveryFixture(b *testing.B, roots int) string {
	b.Helper()
	process := exec.Command("sleep", "3600")
	if err := process.Start(); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() { _ = process.Process.Kill(); _ = process.Wait() })
	home := b.TempDir()
	b.Setenv("HOME", home)
	b.Setenv("CLAUDE_CONFIG_DIR", "")
	dirs := []string{filepath.Join(home, ".claude")}
	for i := 1; i < roots; i++ {
		dirs = append(dirs, filepath.Join(home, fmt.Sprintf("claude-%d", i)))
	}
	if roots > 1 {
		data, _ := json.Marshal(map[string]any{"claudeConfigDirs": dirs[1:]})
		writeDiscoveryFile(b, filepath.Join(home, ".agenthail", "config.json"), string(data))
	}
	at := time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano)
	for project := 0; project < 40; project++ {
		root := dirs[project%roots]
		for n := 0; n < 25; n++ {
			id := fmt.Sprintf("00000000-0000-4000-8000-%04d%08d", project, n)
			line, _ := json.Marshal(map[string]any{"type": "user", "uuid": "u" + id, "sessionId": id, "timestamp": at, "message": map[string]any{"role": "user", "content": "question"}})
			writeDiscoveryFile(b, filepath.Join(root, "projects", fmt.Sprintf("project-%d", project), id+".jsonl"), string(line)+"\n")
			if n == 0 {
				record, _ := json.Marshal(map[string]any{"pid": process.Process.Pid, "sessionId": id, "cwd": "/tmp", "status": "idle", "bridgeSessionId": "session_" + id, "startedAt": time.Now().UnixMilli()})
				writeDiscoveryFile(b, filepath.Join(root, "sessions", fmt.Sprintf("%d.json", 100000+project)), string(record))
			}
		}
	}
	return home
}

func writeDiscoveryFile(b *testing.B, path, content string) {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		b.Fatal(err)
	}
}

func benchmarkDiscovery(b *testing.B, roots int, cold bool) {
	home := discoveryFixture(b, roots)
	ctx := context.Background()
	claude := NewClaude(signin.Fixed{Browser: signin.Chrome}, home)
	if sessions, err := claude.List(ctx); err != nil || len(sessions) != 40 {
		b.Fatalf("sessions=%d err=%v", len(sessions), err)
	}
	for b.Loop() {
		if cold {
			claude = NewClaude(signin.Fixed{Browser: signin.Chrome}, home)
		}
		if _, err := claude.List(ctx); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkClaudeDiscoveryOneRoot(b *testing.B)      { benchmarkDiscovery(b, 1, false) }
func BenchmarkClaudeDiscoveryOneRootCold(b *testing.B)  { benchmarkDiscovery(b, 1, true) }
func BenchmarkClaudeDiscoveryTwoRoots(b *testing.B)     { benchmarkDiscovery(b, 2, false) }
func BenchmarkClaudeDiscoveryTwoRootsCold(b *testing.B) { benchmarkDiscovery(b, 2, true) }

package codextest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const RealMinimumVersion = "0.151.0"

type Real struct {
	Home      string
	CodexHome string
	Restart   func()
}

func (r *Real) Socket() string { return SocketPath(r.CodexHome) }

// StartReal leaves the process environment alone, so several can run in one test.
func StartReal(t testing.TB, model http.Handler) *Real {
	t.Helper()
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	out, err := exec.Command("codex", "--version").Output()
	if err != nil || !atLeast(strings.TrimPrefix(strings.TrimSpace(string(out)), "codex-cli "), RealMinimumVersion) {
		t.Skipf("codex %q is older than %s", out, RealMinimumVersion)
	}
	server := httptest.NewServer(model)
	t.Cleanup(server.Close)
	home, err := os.MkdirTemp("/tmp", "ah-cx-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(home) })
	real := &Real{Home: home, CodexHome: filepath.Join(home, "codex")}
	for _, dir := range []string{real.CodexHome, filepath.Join(home, "tmp"), filepath.Dir(real.Socket())} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	config := fmt.Sprintf("model = \"probe-model\"\nmodel_provider = \"mock\"\n[model_providers.mock]\nname = \"mock\"\nbase_url = \"%s/v1\"\nwire_api = \"responses\"\nenv_key = \"MOCK_KEY\"\n", server.URL)
	if err := os.WriteFile(filepath.Join(real.CodexHome, "config.toml"), []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	stop := real.start(t)
	t.Cleanup(func() { stop() })
	real.Restart = func() {
		stop()
		_ = os.Remove(real.Socket())
		stop = real.start(t)
	}
	return real
}

func (r *Real) start(t testing.TB) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "codex", "app-server", "--disable", "plugins", "--listen", "unix://"+r.Socket())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = []string{"HOME=" + r.Home, "CODEX_HOME=" + r.CodexHome, "PATH=" + os.Getenv("PATH"), "MOCK_KEY=x", "TMPDIR=" + filepath.Join(r.Home, "tmp")}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	// codex is a launcher whose app-server runs as its child, so only killing
	// the whole group stops the server.
	stop := func() {
		group := cmd.Process.Pid
		_ = syscall.Kill(-group, syscall.SIGKILL)
		_ = cmd.Wait()
		cancel()
		for deadline := time.Now().Add(5 * time.Second); syscall.Kill(-group, 0) != syscall.ESRCH; time.Sleep(50 * time.Millisecond) {
			if time.Now().After(deadline) {
				t.Errorf("the real app-server's process group %d outlived its stop", group)
				return
			}
		}
	}
	for deadline := time.Now().Add(20 * time.Second); ; time.Sleep(100 * time.Millisecond) {
		if conn, err := net.Dial("unix", r.Socket()); err == nil {
			_ = conn.Close()
			return stop
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatal("the real app-server did not listen on the control socket")
		}
	}
}

type MockModel struct {
	mu       sync.Mutex
	requests int
}

func (m *MockModel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	_, _ = io.Copy(io.Discard, r.Body)
	m.mu.Lock()
	m.requests++
	id := fmt.Sprintf("resp_%d", m.requests)
	m.mu.Unlock()
	w.Header().Set("Content-Type", "text/event-stream")
	send := func(event map[string]any) {
		data, _ := json.Marshal(event)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], data)
	}
	send(map[string]any{"type": "response.created", "response": map[string]any{"id": id}})
	send(map[string]any{"type": "response.output_item.done", "item": map[string]any{"type": "message", "role": "assistant", "id": "msg_" + id, "content": []any{map[string]any{"type": "output_text", "text": "ok"}}}})
	send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "usage": map[string]any{"input_tokens": 9000, "output_tokens": 10, "total_tokens": 9010, "input_tokens_details": map[string]any{"cached_tokens": 0}, "output_tokens_details": map[string]any{"reasoning_tokens": 0}}}})
}

func atLeast(version, minimum string) bool {
	have, ok := versionParts(version)
	want, _ := versionParts(minimum)
	if !ok {
		return false
	}
	for i := range want {
		if have[i] != want[i] {
			return have[i] > want[i]
		}
	}
	return true
}

func versionParts(version string) ([3]int, bool) {
	var parts [3]int
	core, _, _ := strings.Cut(version, "-")
	fields := strings.Split(core, ".")
	if len(fields) != 3 {
		return parts, false
	}
	for i, field := range fields {
		n, err := strconv.Atoi(field)
		if err != nil {
			return parts, false
		}
		parts[i] = n
	}
	return parts, true
}

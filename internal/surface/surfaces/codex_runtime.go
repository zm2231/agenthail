package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

type codexDaemonVersion struct {
	Status     string `json:"status"`
	Backend    string `json:"backend"`
	SocketPath string `json:"socketPath"`
}

func codexBinary() (string, error) {
	if configured := strings.TrimSpace(os.Getenv("AGENTHAIL_CODEX_BIN")); configured != "" {
		path, err := exec.LookPath(configured)
		if err != nil {
			return "", fmt.Errorf("AGENTHAIL_CODEX_BIN %q is not executable", configured)
		}
		return path, nil
	}
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, _ := os.UserHomeDir()
		home = filepath.Join(userHome, ".codex")
	}
	managed := filepath.Join(home, "packages", "standalone", "current", "codex")
	if info, err := os.Stat(managed); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
		return managed, nil
	}
	if path, err := exec.LookPath("codex"); err == nil {
		return path, nil
	}
	userHome, _ := os.UserHomeDir()
	for _, path := range []string{
		"/Applications/ChatGPT.app/Contents/Resources/codex",
		filepath.Join(userHome, "Applications", "ChatGPT.app", "Contents", "Resources", "codex"),
	} {
		if info, err := os.Stat(path); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("Codex standalone runtime was not found at %s; install it from https://chatgpt.com/codex/install.sh or set AGENTHAIL_CODEX_BIN", managed)
}

func runCodexDaemon(ctx context.Context, action string) ([]byte, error) {
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	binary, err := codexBinary()
	if err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(ctx, binary, "app-server", "daemon", action).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("codex app-server daemon %s: %w (%s)", action, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (c *Codex) RuntimeStatus(ctx context.Context) surface.RuntimeStatus {
	if !c.managed {
		return surface.RuntimeStatus{}
	}
	status := surface.RuntimeStatus{
		Name:        "Codex managed app-server",
		Remediation: "run 'agenthail daemon install'",
	}
	output, err := runCodexDaemon(ctx, "version")
	if err != nil {
		status.Detail = err.Error()
		return status
	}
	var version codexDaemonVersion
	if err := json.Unmarshal(output, &version); err != nil {
		status.Detail = fmt.Sprintf("parse Codex managed app-server status: %v", err)
		return status
	}
	status.Reachable = version.Status == "running"
	status.Backend = version.Backend
	status.Durable = version.Backend != "" && version.Backend != "pid"
	if status.Reachable && !status.Durable {
		status.Detail = "reachable but not supervised across reboot"
	}
	if status.Durable {
		status.Remediation = ""
	}
	return status
}

func (c *Codex) EnsureRuntime(ctx context.Context) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	return c.ensureRuntime(ctx, false)
}

func (c *Codex) ensureRuntime(ctx context.Context, force bool) error {
	if !c.managed {
		return nil
	}
	if _, err := os.Stat(managedCodexSocketPath()); err == nil && !force && c.runtimeReady {
		return nil
	}
	if _, err := runCodexDaemon(ctx, "bootstrap"); err != nil {
		c.runtimeReady = false
		return fmt.Errorf("managed Codex app-server is unavailable: %w; install the Codex standalone runtime, then run 'agenthail daemon install'", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(managedCodexSocketPath()); err == nil {
			c.runtimeReady = true
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("managed Codex app-server did not become ready: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("managed Codex app-server did not create %s; run 'agenthail doctor'", managedCodexSocketPath())
}

func (c *Codex) openManaged(ctx context.Context) (codexClient, error) {
	client, err := dialManagedCodex(ctx)
	if err == nil {
		return client, nil
	}
	c.runtimeMu.Lock()
	c.runtimeReady = false
	ensureErr := c.ensureRuntime(ctx, true)
	c.runtimeMu.Unlock()
	if ensureErr != nil {
		return nil, fmt.Errorf("%v; recovery failed: %w", err, ensureErr)
	}
	client, retryErr := dialManagedCodex(ctx)
	if retryErr != nil {
		return nil, fmt.Errorf("managed Codex app-server did not recover: %w", retryErr)
	}
	return client, nil
}

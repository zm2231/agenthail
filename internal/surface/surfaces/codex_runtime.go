package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

var codexDaemonStartTimeout = 60 * time.Second

type codexDaemonVersion struct {
	Status     string `json:"status"`
	Backend    string `json:"backend"`
	SocketPath string `json:"socketPath"`
}

type codexDaemonSettings struct {
	RemoteControlEnabled bool `json:"remoteControlEnabled"`
}

func codexDaemonSettingsPath() string {
	return filepath.Join(codexHome(), "app-server-daemon", "settings.json")
}

func codexRemoteControlEnabled() (bool, error) {
	data, err := os.ReadFile(codexDaemonSettingsPath())
	if err != nil {
		return false, err
	}
	var settings codexDaemonSettings
	if err := json.Unmarshal(data, &settings); err != nil {
		return false, err
	}
	return settings.RemoteControlEnabled, nil
}

const (
	codexLaunchRemediation       = "run 'agenthail launch codex' (quit Codex first if it is already open)"
	codexStartManagedRemediation = "run 'agenthail codex' to start the managed Codex app-server"
	codexInstallRemediation      = "install the standalone Codex runtime: " + CodexStandaloneInstallCommand
)

var ErrCodexStandaloneMissing = errors.New("standalone Codex runtime is not installed")

func ManagedCodexBinary() (string, error) {
	install := DetectCodexInstallation()
	if install.Standalone == "" {
		return "", fmt.Errorf("%w: %s; %s", ErrCodexStandaloneMissing, install.standaloneMissingNote(), codexInstallRemediation)
	}
	return install.Standalone, nil
}

func RestartManagedCodexRuntime(ctx context.Context) error {
	_, err := runCodexDaemon(ctx, "restart")
	return err
}

func runCodexDaemon(ctx context.Context, action string) ([]byte, error) {
	commandCtx := ctx
	cancel := func() {}
	timeout := 10 * time.Second
	if action == "start" || action == "restart" {
		timeout = codexDaemonStartTimeout
		commandCtx, cancel = context.WithTimeout(context.Background(), timeout)
	} else if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		commandCtx, cancel = context.WithTimeout(ctx, timeout)
	}
	defer cancel()
	binary, err := ManagedCodexBinary()
	if err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(commandCtx, binary, "app-server", "daemon", action).CombinedOutput()
	if err != nil {
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return output, fmt.Errorf("codex app-server daemon %s timed out after %s; Agenthail ended the command (%s)", action, timeout, strings.TrimSpace(string(output)))
		}
		return output, fmt.Errorf("codex app-server daemon %s: %w (%s)", action, err, strings.TrimSpace(string(output)))
	}
	return output, nil
}

func (c *Codex) RuntimeStatus(ctx context.Context) surface.RuntimeStatus {
	install := DetectCodexInstallation()
	var notes []surface.RuntimeNote
	if c.managed && install.Standalone == "" {
		notes = append(notes, surface.RuntimeNote{Problem: surface.RuntimeStandaloneMissing, Message: install.standaloneMissingNote(), Remediation: codexInstallRemediation})
	}
	desktopCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	desktopErr := c.DesktopReady(desktopCtx)
	cancel()
	if desktopErr == nil {
		return surface.RuntimeStatus{
			Name:      "Codex Desktop bridge",
			Detail:    "renderer read path reachable; direct input is verified per target before delivery",
			Reachable: true,
			Durable:   true,
			Backend:   "desktop",
			Notes:     notes,
		}
	}
	bridge := surface.RuntimeStatus{
		Name:        "Codex Desktop bridge",
		Problem:     surface.RuntimeBridgeUnavailable,
		Detail:      desktopErr.Error(),
		Remediation: codexLaunchRemediation,
		Notes:       notes,
	}
	if !c.managed {
		return bridge
	}
	if install.Standalone == "" {
		if install.Desktop != "" {
			return bridge
		}
		return surface.RuntimeStatus{
			Name:        "Codex managed app-server",
			Problem:     surface.RuntimeStandaloneMissing,
			Detail:      install.standaloneMissingNote() + "; Codex Desktop is not installed either",
			Remediation: codexInstallRemediation,
		}
	}
	managed := surface.RuntimeStatus{Name: "Codex managed app-server", Problem: surface.RuntimeStopped, Remediation: codexStartManagedRemediation}
	output, err := runCodexDaemon(ctx, "version")
	if err != nil {
		managed.Detail = err.Error()
	} else {
		var version codexDaemonVersion
		if err := json.Unmarshal(output, &version); err != nil {
			managed.Detail = fmt.Sprintf("parse Codex managed app-server status: %v", err)
		} else if version.Status != "running" {
			managed.Detail = fmt.Sprintf("managed Codex app-server is %s", version.Status)
		} else {
			managed.Reachable = true
			managed.Backend = version.Backend
			managed.Problem = ""
			managed.Remediation = ""
			supervisor := strings.TrimSpace(os.Getenv("AGENTHAIL_DAEMON_SUPERVISOR"))
			launchdService := strings.TrimSpace(os.Getenv("XPC_SERVICE_NAME")) == "com.agenthail.daemon"
			managed.Durable = version.Backend != "" && version.Backend != "pid" || supervisor == "homebrew" || launchdService
			if !managed.Durable {
				managed.Problem = surface.RuntimeUnsupervised
				managed.Detail = "reachable but not supervised across reboot"
				managed.Remediation = "run 'agenthail daemon install'"
			}
			if install.Desktop != "" {
				managed.Notes = append(managed.Notes, surface.RuntimeNote{Problem: surface.RuntimeBridgeUnavailable, Message: "Codex Desktop conversations need the Desktop bridge: " + desktopErr.Error(), Remediation: codexLaunchRemediation})
			}
			return managed
		}
	}
	if install.Desktop != "" {
		bridge.Notes = append(bridge.Notes, surface.RuntimeNote{Problem: surface.RuntimeStopped, Message: "the managed Codex app-server is not running: " + managed.Detail, Remediation: codexStartManagedRemediation})
		return bridge
	}
	return managed
}

func (c *Codex) EnsureRuntime(ctx context.Context) error {
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	return c.ensureRuntime(ctx)
}

func (c *Codex) ensureRuntime(ctx context.Context) error {
	if !c.managed {
		return nil
	}
	if enabled, err := codexRemoteControlEnabled(); err != nil || !enabled {
		if _, err := runCodexDaemon(ctx, "enable-remote-control"); err != nil {
			return fmt.Errorf("enable Codex app-server remote control: %w", err)
		}
	}
	if _, err := os.Stat(managedCodexSocketPath()); err == nil {
		return nil
	}
	if _, err := runCodexDaemon(ctx, "start"); err != nil {
		return fmt.Errorf("managed Codex app-server is unavailable: %w", err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(managedCodexSocketPath()); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("managed Codex app-server did not become ready: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	return fmt.Errorf("managed Codex app-server did not create %s; run 'agenthail codex' again or inspect 'agenthail doctor'", managedCodexSocketPath())
}

func (c *Codex) openManaged(ctx context.Context) (codexClient, error) {
	if err := c.EnsureRuntime(ctx); err != nil {
		return nil, err
	}
	client, err := dialManagedCodex(ctx)
	if err != nil {
		return nil, fmt.Errorf("managed Codex app-server is unreachable: %w; restart it from Codex only after closing active remote sessions", err)
	}
	return client, nil
}

func (c *Codex) openExistingManaged(ctx context.Context) (codexClient, error) {
	client, err := dialManagedCodex(ctx)
	if err != nil {
		return nil, fmt.Errorf("managed Codex app-server is unavailable: %w", err)
	}
	return client, nil
}

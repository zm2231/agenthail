package surfaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
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

const (
	codexLaunchRemediation  = "run 'agenthail launch codex' (quit Codex first if it is already open)"
	codexDaemonRemediation  = "start Codex's app-server daemon; Agenthail also starts it for the next managed conversation"
	codexInstallRemediation = "install Codex: " + CodexInstallCommand
)

var ErrCodexMissing = errors.New("no Codex CLI found to run Codex's app-server daemon")

// CodexDaemonCLI returns the Codex CLI that runs `codex app-server daemon`
// lifecycle commands.
func CodexDaemonCLI() (string, error) {
	install := DetectCodexInstallation()
	if install.Daemon == "" {
		return "", fmt.Errorf("%w (looked in %s and PATH); %s", ErrCodexMissing, strings.Join(codexDaemonPackageBinaries(), ", "), codexInstallRemediation)
	}
	return install.Daemon, nil
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
	binary, err := CodexDaemonCLI()
	if err != nil {
		return nil, err
	}
	output, err := exec.CommandContext(commandCtx, binary, "app-server", "daemon", action).Output()
	if err != nil {
		detail := strings.TrimSpace(string(output))
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			detail = strings.TrimSpace(detail + " " + strings.TrimSpace(string(exitErr.Stderr)))
		}
		if errors.Is(commandCtx.Err(), context.DeadlineExceeded) {
			return output, fmt.Errorf("codex app-server daemon %s timed out after %s; Agenthail ended the command (%s)", action, timeout, detail)
		}
		return output, fmt.Errorf("codex app-server daemon %s: %w (%s)", action, err, detail)
	}
	return output, nil
}

func (c *Codex) RuntimeStatus(ctx context.Context) surface.RuntimeStatus {
	install := DetectCodexInstallation()
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
		}
	}
	bridge := surface.RuntimeStatus{
		Name:        "Codex Desktop bridge",
		Problem:     surface.RuntimeBridgeUnavailable,
		Detail:      desktopErr.Error(),
		Remediation: codexLaunchRemediation,
	}
	if !c.managed {
		return bridge
	}
	if install.Daemon == "" {
		if install.Desktop != "" {
			return bridge
		}
		return surface.RuntimeStatus{
			Name:        "Codex app-server daemon",
			Problem:     surface.RuntimeCodexMissing,
			Detail:      ErrCodexMissing.Error() + " and Codex Desktop is not installed",
			Remediation: codexInstallRemediation,
		}
	}
	daemon := surface.RuntimeStatus{Name: "Codex app-server daemon", Problem: surface.RuntimeStopped, Remediation: codexDaemonRemediation}
	output, err := runCodexDaemon(ctx, "version")
	var version codexDaemonVersion
	switch {
	case err != nil:
		daemon.Detail = err.Error()
	case json.Unmarshal(output, &version) != nil:
		daemon.Detail = fmt.Sprintf("parse Codex app-server daemon status: %s", strings.TrimSpace(string(output)))
	case version.Status != "running":
		daemon.Detail = fmt.Sprintf("Codex app-server daemon is %s", version.Status)
	default:
		daemon.Reachable = true
		daemon.Backend = version.Backend
		daemon.Problem = ""
		daemon.Remediation = ""
		supervisor := strings.TrimSpace(os.Getenv("AGENTHAIL_DAEMON_SUPERVISOR"))
		launchdService := strings.TrimSpace(os.Getenv("XPC_SERVICE_NAME")) == "com.agenthail.daemon"
		daemon.Durable = version.Backend != "" && version.Backend != "pid" || supervisor == "homebrew" || launchdService
		if !daemon.Durable {
			daemon.Problem = surface.RuntimeUnsupervised
			daemon.Detail = "reachable but not supervised across reboot"
			daemon.Remediation = "run 'agenthail daemon install'"
		}
	}
	if install.Desktop == "" {
		return daemon
	}
	if daemon.Reachable {
		daemon.Notes = append(daemon.Notes, surface.RuntimeNote{Problem: surface.RuntimeBridgeUnavailable, Message: "Codex Desktop conversations need the Desktop bridge: " + desktopErr.Error(), Remediation: codexLaunchRemediation})
		return daemon
	}
	bridge.Notes = append(bridge.Notes, surface.RuntimeNote{Problem: surface.RuntimeStopped, Message: "Codex's app-server daemon is not running: " + daemon.Detail, Remediation: codexDaemonRemediation})
	return bridge
}

// EnsureRuntime starts Codex's app-server daemon when it is not serving its
// control socket.
func (c *Codex) EnsureRuntime(ctx context.Context) error {
	client, err := c.openManaged(ctx)
	if err != nil {
		return err
	}
	return client.Close()
}

// openManaged is the only path that starts Codex's app-server daemon. It is
// for managed-route writes and creation; discovery and reads use dialManaged.
func (c *Codex) openManaged(ctx context.Context) (codexClient, error) {
	if !c.managed {
		return nil, errors.New("Codex's app-server daemon is disabled for a remote Codex Desktop endpoint")
	}
	if client, err := dialManagedCodex(ctx); err == nil {
		return client, nil
	}
	c.runtimeMu.Lock()
	defer c.runtimeMu.Unlock()
	if client, err := dialManagedCodex(ctx); err == nil {
		return client, nil
	}
	if _, err := runCodexDaemon(ctx, "start"); err != nil {
		return nil, fmt.Errorf("start Codex's app-server daemon: %w", err)
	}
	client, err := dialManagedCodex(ctx)
	if err != nil {
		return nil, fmt.Errorf("Codex's app-server daemon started but is unreachable: %w", err)
	}
	return client, nil
}

// dialManaged connects to a running daemon and never starts one.
func (c *Codex) dialManaged(ctx context.Context) (codexClient, error) {
	if !c.managed {
		return nil, errors.New("Codex's app-server daemon is disabled for a remote Codex Desktop endpoint")
	}
	client, err := dialManagedCodex(ctx)
	if err != nil {
		return nil, err
	}
	return client, nil
}

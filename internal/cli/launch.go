package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/codexconfig"
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
)

func (a *App) cmdLaunch(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 1 {
		return fmt.Errorf("usage: agenthail launch [codex]")
	}
	target := "codex"
	if len(positional) > 0 {
		target = positional[0]
	}
	switch target {
	case "codex":
		return launchCodex(a.adapters().ForSurface(surface.KindCodex))
	case "claude":
		return fmt.Errorf("claude must be launched manually (open the app or visit claude.ai/code)")
	case "notion":
		return fmt.Errorf("notion must be launched manually (open app.notion.com in your browser and log in)")
	default:
		return fmt.Errorf("unknown surface '%s' (try: codex)", target)
	}
}

func launchCodex(codex surface.Surface) error {
	if codex == nil {
		return fmt.Errorf("Codex surface is unavailable")
	}
	install := surfaces.DetectCodexInstallation()
	exe := install.Desktop
	if exe == "" {
		message := fmt.Sprintf("Codex Desktop is not installed (tried %s)", strings.Join(surfaces.CodexDesktopExecutables, ", "))
		if install.Daemon != "" {
			message += "; Agenthail creates and drives Codex conversations on Codex's app-server daemon without it"
		}
		return errors.New(message)
	}

	pid := findCodexPID()
	if pid > 0 {
		if err := probeCodexDesktop(codex); err == nil {
			fmt.Println("Codex is ready for Agenthail")
			return nil
		}
		return fmt.Errorf("Codex is already open without Agenthail's Desktop bridge; quit Codex, then run 'agenthail launch codex'")
	}

	cmd := exec.Command(exe, codexLaunchArgs()...)
	cmd.Env = surface.AgentEnv(os.Environ())
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("launch codex: %w", err)
	}
	pid = cmd.Process.Pid
	cmd.Process.Release()
	fmt.Println("Opening Codex and waiting for it to connect")
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if err := probeCodexDesktop(codex); err == nil {
			fmt.Println("Codex is ready for Agenthail")
			fmt.Println("Run 'agenthail doctor' to check every connection")
			return nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("Codex opened but its Desktop bridge did not become ready; run 'agenthail doctor' for details")
}

const codexLaunchProbeTimeout = 5 * time.Second

func probeCodexAppServer(codex surface.Surface) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexLaunchProbeTimeout)
	defer cancel()
	if checker, ok := codex.(surface.ReadinessChecker); ok {
		return checker.Ready(ctx)
	}
	_, err := codex.List(ctx)
	return err
}

func probeCodexDesktop(codex surface.Surface) error {
	ctx, cancel := context.WithTimeout(context.Background(), codexLaunchProbeTimeout)
	defer cancel()
	if checker, ok := codex.(interface{ DesktopReady(context.Context) error }); ok {
		return checker.DesktopReady(ctx)
	}
	return probeCodexAppServer(codex)
}

func codexLaunchArgs() []string {
	port := codexconfig.LaunchRemoteDebuggingPort()
	return []string{
		"--no-first-run",
		"--no-default-browser-check",
		"--remote-debugging-address=127.0.0.1",
		"--remote-debugging-port=" + port,
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func findCodexPID() int {
	output, err := exec.Command("ps", "-axo", "pid=,command=").Output()
	if err != nil {
		return 0
	}
	return selectCodexPID(string(output), surfaces.CodexDesktopExecutables)
}

func selectCodexPID(output string, expectedExecutables []string) int {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err != nil || pid <= 0 {
			continue
		}
		command := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), fields[0]))
		for _, expectedExecutable := range expectedExecutables {
			if command == expectedExecutable || strings.HasPrefix(command, expectedExecutable+" ") {
				return pid
			}
		}
	}
	return 0
}

func truncate(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n == 1 {
		return "…"
	}
	return string(runes[:n-1]) + "…"
}

func messageText(text string) (string, error) {
	if text != "-" {
		return text, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

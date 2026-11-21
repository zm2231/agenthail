package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemonctl"
	"github.com/zm2231/agenthail/internal/dashconfig"
	"github.com/zm2231/agenthail/internal/poll"
	"github.com/zm2231/agenthail/internal/surface/codexsurface"
)

const daemonLogMaxBytes = 1024 * 1024

func (a *App) daemonStart() error {
	return a.service().Start()
}

func (a *App) daemonStop() error {
	return a.service().Stop()
}

func (a *App) daemonRestart() error {
	return a.service().Restart()
}

func (a *App) daemonStatus() error {
	pid, ok := daemonctl.IsRunning()
	if !ok {
		for _, line := range a.service().StatusLines() {
			fmt.Println(line)
		}
		return fmt.Errorf("daemon is not running (start it with 'agenthail daemon start')")
	}
	fmt.Printf("daemon: running (pid %d)\n", pid)
	logPath := daemonStatusLogPath()
	if info, err := os.Stat(logPath); err == nil {
		fmt.Printf("log: %s (%s)\n", logPath, humanSize(info.Size()))
	}
	for _, line := range a.service().StatusLines() {
		fmt.Println(line)
	}
	return nil
}

func daemonStatusLogPath() string {
	if configured := strings.TrimSpace(os.Getenv("AGENTHAIL_DAEMON_LOG")); configured != "" {
		return configured
	}
	executable, err := os.Executable()
	if err == nil {
		return daemonLogPathForExecutable(executable)
	}
	return daemonctl.LogFilePath()
}

func daemonLogPathForExecutable(executable string) string {
	if marker := strings.Index(executable, "/Cellar/agenthail/"); marker > 0 {
		return filepath.Join(executable[:marker], "var", "log", "agenthail.log")
	}
	return daemonctl.LogFilePath()
}

func (a *App) daemonRun() error {
	if a.RunDaemon == nil {
		return errors.New("this build cannot run the daemon")
	}
	// Opening can migrate, so a daemon that may not run must find that out first.
	release, err := daemonctl.HoldLock()
	if err != nil {
		return err
	}
	defer release()
	began := time.Now()
	return a.RunDaemon(DaemonRun{
		Registry:    a.registry(),
		Surfaces:    a.allSurfaces(),
		Build:       a.build(),
		HostVersion: a.Version + "+" + a.Revision,
		Began:       began,
	})
}

func (a *App) cmdDashboard(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 2 {
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status|config|remote [status|off]|token rotate] [options]")
	}
	action := "open"
	if len(positional) > 0 {
		action = positional[0]
	}
	config, err := dashconfig.Load()
	if err != nil {
		return err
	}
	switch action {
	case "enable":
		config.Enabled = true
		if err := dashconfig.Save(config); err != nil {
			return err
		}
		if err := a.restartDaemonForDashboard(); err != nil {
			return err
		}
		if err := waitForDashboard(config.Listen, 15*time.Second); err != nil {
			return err
		}
		if hasFlag(args, "--no-open") {
			fmt.Println("dashboard enabled")
			return nil
		}
		return a.openDashboard()
	case "disable":
		config.Enabled = false
		if err := dashconfig.Save(config); err != nil {
			return err
		}
		if _, running := daemonctl.IsRunning(); running {
			if err := a.restartDaemonForDashboard(); err != nil {
				return err
			}
		}
		fmt.Println("dashboard disabled")
		return nil
	case "status":
		if !config.Enabled {
			fmt.Println("dashboard: disabled (run 'agenthail dashboard enable')")
			return nil
		}
		fmt.Printf("dashboard: enabled on %s\n", config.Listen)
		fmt.Printf("Codex current window: %dh\n", config.CodexRecentHours)
		if _, running := daemonctl.IsRunning(); !running {
			fmt.Println("daemon: not running (start it with 'agenthail daemon start')")
		} else {
			fmt.Printf("address: %s (sign in with 'agenthail dashboard')\n", dashconfig.BaseURL(config))
		}
		return nil
	case "config":
		rawHours := flagVal(args, "--codex-recent-hours")
		if rawHours == "" {
			fmt.Printf("Codex current window: %dh\n", config.CodexRecentHours)
			return nil
		}
		hours, parseErr := strconv.Atoi(rawHours)
		if parseErr != nil {
			return fmt.Errorf("Codex recent hours must be a whole number")
		}
		if hours < 1 {
			return fmt.Errorf("Codex recent hours must be at least 1")
		}
		config.CodexRecentHours = hours
		if err := dashconfig.Save(config); err != nil {
			return err
		}
		fmt.Printf("Codex current window: %dh\n", config.CodexRecentHours)
		return nil
	case "remote":
		return a.cmdDashboardRemote(args, positional[1:])
	case "token":
		return a.cmdDashboardToken(positional[1:])
	case "open":
		if !config.Enabled {
			return fmt.Errorf("dashboard is disabled; run 'agenthail dashboard enable'")
		}
		if _, running := daemonctl.IsRunning(); !running {
			return fmt.Errorf("daemon is not running; start it with 'agenthail daemon start'")
		}
		return a.openDashboard()
	default:
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status|config|remote [status|off]|token rotate] [options]")
	}
}

func (a *App) openDashboard() error {
	link, err := a.createDashboardLoginLink(false)
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		fmt.Printf("sign in: %s (%s)\n", link.URL, link.validity())
		return nil
	}
	if err := exec.Command("open", link.URL).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	fmt.Println("opened the dashboard in your browser")
	return nil
}

func (a *App) restartDaemonForDashboard() error {
	return a.service().Restart()
}

func waitForDashboard(listen string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := poll.Until(ctx, 100*time.Millisecond, func() (bool, error) {
		return dashboardAnswers(listen), nil
	}); err == nil {
		return nil
	}
	return fmt.Errorf("dashboard did not become ready on http://%s; inspect %s", listen, daemonStatusLogPath())
}

func dashboardAnswers(listen string) bool {
	client := &http.Client{Timeout: 500 * time.Millisecond}
	response, err := client.Get("http://" + listen + "/")
	if err != nil {
		return false
	}
	response.Body.Close()
	return response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusOK || response.StatusCode == http.StatusFound
}

func (a *App) daemonInstallService() error {
	if err := a.service().Install(); err != nil {
		return err
	}
	if err := claudeMCPAt("install"); err != nil {
		fmt.Printf("Claude sessions started outside Agenthail lack its tools until this is fixed and agenthail mcp install runs: %s\n", err)
	}
	if err := codexMCPAt("install"); err != nil {
		fmt.Printf("Codex sessions started outside Agenthail lack its tools until this is fixed and agenthail mcp install runs: %s\n", err)
	}
	return nil
}

func (a *App) daemonUninstallService() error {
	if err := a.service().Uninstall(); err != nil {
		return err
	}
	if err := claudeMCPAt("uninstall"); err != nil {
		fmt.Printf("Claude may show a failed agenthail MCP server until it is removed with agenthail mcp uninstall: %s\n", err)
	}
	if err := codexMCPAt("uninstall"); err != nil {
		fmt.Printf("Codex may show a failed %s MCP server until it is removed with agenthail mcp uninstall: %s\n", codexsurface.CodexRelayServerName, err)
	}
	return nil
}

func humanSize(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%dB", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1fKB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1fMB", float64(n)/(1024*1024))
	}
}

func printCodexSetupNote() {
	if _, err := codexsurface.CodexDaemonCLI(codexsurface.CodexHomeFromEnv()); err != nil {
		fmt.Printf("Codex: %s\n", err)
	}
}

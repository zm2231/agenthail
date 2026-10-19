package cli

import (
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/claudehost"
	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/surface/surfaces"
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
	pid, ok := daemon.IsRunning()
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
	return daemon.LogFilePath()
}

func daemonLogPathForExecutable(executable string) string {
	if marker := strings.Index(executable, "/Cellar/agenthail/"); marker > 0 {
		return filepath.Join(executable[:marker], "var", "log", "agenthail.log")
	}
	return daemon.LogFilePath()
}

func (a *App) daemonRun() error {
	for _, e := range a.Surfaces {
		_ = e
	}
	d := daemon.New(a.Registry, a.allSurfaces())
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	hostVersion := a.Version + "+" + a.Revision
	d.SetClaudeHost(&claudehost.Manager{Home: home, Launch: func() (claudehost.Launch, error) {
		return surfaces.ClaudeHostLaunch(home, hostVersion)
	}})
	return d.RunWithSignal()
}

func (a *App) cmdDashboard(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 2 {
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status|config|remote [status|off]] [options]")
	}
	action := "open"
	if len(positional) > 0 {
		action = positional[0]
	}
	config, err := daemon.LoadDashboardConfig()
	if err != nil {
		return err
	}
	switch action {
	case "enable":
		config.Enabled = true
		if err := daemon.SaveDashboardConfig(config); err != nil {
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
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return err
		}
		if _, running := daemon.IsRunning(); running {
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
		url, err := daemon.DashboardURL()
		if err != nil {
			return err
		}
		fmt.Printf("dashboard: enabled on %s\n", config.Listen)
		fmt.Printf("Codex current window: %dh\n", config.CodexRecentHours)
		if _, running := daemon.IsRunning(); !running {
			fmt.Println("daemon: not running (start it with 'agenthail daemon start')")
		} else {
			fmt.Printf("open: %s\n", url)
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
		if err := daemon.SaveDashboardConfig(config); err != nil {
			return err
		}
		fmt.Printf("Codex current window: %dh\n", config.CodexRecentHours)
		return nil
	case "remote":
		return a.cmdDashboardRemote(args, positional[1:])
	case "open":
		if !config.Enabled {
			return fmt.Errorf("dashboard is disabled; run 'agenthail dashboard enable'")
		}
		if _, running := daemon.IsRunning(); !running {
			return fmt.Errorf("daemon is not running; start it with 'agenthail daemon start'")
		}
		return a.openDashboard()
	default:
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status|config|remote [status|off]] [options]")
	}
}

func (a *App) openDashboard() error {
	url, err := daemon.DashboardURL()
	if err != nil {
		return err
	}
	if runtime.GOOS != "darwin" {
		fmt.Printf("dashboard: %s\n", url)
		return nil
	}
	if err := exec.Command("open", url).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	fmt.Printf("opened dashboard: %s\n", url)
	return nil
}

func (a *App) restartDaemonForDashboard() error {
	return a.service().Restart()
}

func waitForDashboard(listen string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		response, err := client.Get("http://" + listen + "/")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusOK || response.StatusCode == http.StatusFound {
				return nil
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("dashboard did not become ready on http://%s; inspect %s", listen, daemonStatusLogPath())
}

func (a *App) daemonInstallService() error {
	return a.service().Install()
}

func (a *App) daemonUninstallService() error {
	return a.service().Uninstall()
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
	if _, err := surfaces.CodexDaemonCLI(); err != nil {
		fmt.Printf("Codex: %s\n", err)
	}
}

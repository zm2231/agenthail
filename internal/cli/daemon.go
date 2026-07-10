package cli

import (
	"fmt"
	"html"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

const (
	daemonLaunchdLabel = "com.agenthail.daemon"
	daemonLogMaxBytes  = 1024 * 1024
)

func (a *App) daemonStart() error {
	if pid, ok := daemon.IsRunning(); ok {
		return fmt.Errorf("daemon already running (pid %d)", pid)
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate agenthail binary: %w", err)
	}
	logPath := daemon.LogFilePath()
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	cmd := exec.Command(exe, "daemon-run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		logFile.Close()
		return fmt.Errorf("spawn daemon: %w", err)
	}
	pid := cmd.Process.Pid
	cmd.Process.Release()
	logFile.Close()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if runningPID, ok := daemon.IsRunning(); ok && runningPID == pid {
			fmt.Printf("daemon started (pid %d)\n", pid)
			fmt.Printf("log: %s\n", logPath)
			fmt.Printf("stop: agenthail daemon stop\n")
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	data, _ := os.ReadFile(logPath)
	return fmt.Errorf("daemon failed readiness check: %s", string(data))
}

func (a *App) daemonStop() error {
	pid, ok := daemon.IsRunning()
	if !ok {
		fmt.Println("daemon not running")
		return nil
	}
	if servicePID, loaded := daemonServicePID(); loaded && servicePID == pid {
		return fmt.Errorf("daemon is supervised by launchd; use 'agenthail daemon uninstall' to stop and remove the service")
	}
	if err := daemon.Stop(); err != nil {
		return err
	}
	fmt.Printf("daemon stopped (was pid %d)\n", pid)
	return nil
}

func (a *App) daemonStatus() error {
	pid, ok := daemon.IsRunning()
	if !ok {
		return fmt.Errorf("daemon is not running (start it with 'agenthail daemon start')")
	}
	fmt.Printf("daemon: running (pid %d)\n", pid)
	logPath := daemon.LogFilePath()
	if info, err := os.Stat(logPath); err == nil {
		fmt.Printf("log: %s (%s)\n", logPath, humanSize(info.Size()))
	}
	return nil
}

func (a *App) daemonRun() error {
	for _, e := range a.Surfaces {
		_ = e
	}
	d := daemon.New(a.Registry, a.allSurfaces())
	return d.RunWithSignal()
}

func (a *App) cmdDashboard(args []string) error {
	positional := stripFlags(args)
	if len(positional) > 1 {
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status] [--no-open]")
	}
	action := "open"
	if len(positional) == 1 {
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
		if _, running := daemon.IsRunning(); !running {
			fmt.Println("daemon: not running (start it with 'agenthail daemon start')")
		} else {
			fmt.Printf("open: %s\n", url)
		}
		return nil
	case "open":
		if !config.Enabled {
			return fmt.Errorf("dashboard is disabled; run 'agenthail dashboard enable'")
		}
		if _, running := daemon.IsRunning(); !running {
			return fmt.Errorf("daemon is not running; start it with 'agenthail daemon start'")
		}
		return a.openDashboard()
	default:
		return fmt.Errorf("usage: agenthail dashboard [enable|disable|status] [--no-open]")
	}
}

func (a *App) openDashboard() error {
	url, err := daemon.DashboardURL()
	if err != nil {
		return err
	}
	if err := exec.Command("open", url).Run(); err != nil {
		return fmt.Errorf("open dashboard: %w", err)
	}
	fmt.Printf("opened dashboard: %s\n", url)
	return nil
}

func (a *App) restartDaemonForDashboard() error {
	if daemonServiceLoaded() {
		domain := fmt.Sprintf("gui/%d", os.Getuid())
		if output, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+daemonLaunchdLabel).CombinedOutput(); err != nil {
			return fmt.Errorf("restart launchd daemon for dashboard: %w (%s)", err, strings.TrimSpace(string(output)))
		}
		return nil
	}
	if _, running := daemon.IsRunning(); running {
		if err := daemon.Stop(); err != nil {
			return err
		}
	}
	return a.daemonStart()
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
	return fmt.Errorf("dashboard did not become ready on http://%s; inspect %s", listen, daemon.LogFilePath())
}

func daemonServicePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", daemonLaunchdLabel+".plist")
}

func (a *App) daemonInstallService() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("daemon service installation currently supports macOS launchd only")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+daemonLaunchdLabel).Run()
	// bootout returning does not mean launchd has released the label yet. Wait
	// for that state before replacing the plist; otherwise an upgrade can race
	// bootstrap and fail with a transient EIO.
	for deadline := time.Now().Add(3 * time.Second); daemonServiceLoaded() && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
	}
	if _, running := daemon.IsRunning(); running {
		if err := daemon.Stop(); err != nil {
			return fmt.Errorf("stop existing daemon: %w", err)
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate agenthail binary: %w", err)
	}
	logPath := daemon.LogFilePath()
	if err := rotateLogFile(logPath, daemonLogMaxBytes); err != nil {
		return fmt.Errorf("rotate daemon log: %w", err)
	}
	environment := map[string]string{
		"AGENTHAIL_SIDECAR":            os.Getenv("AGENTHAIL_SIDECAR"),
		"AGENTHAIL_COOKIE_BRIDGE":      os.Getenv("AGENTHAIL_COOKIE_BRIDGE"),
		"AGENTHAIL_PYTHON":             os.Getenv("AGENTHAIL_PYTHON"),
		"AGENTHAIL_CODEX_REMOTE":       codexRemotePort(),
		"AGENTHAIL_CHROME_PROFILE":     envOr("AGENTHAIL_CHROME_PROFILE", "Default"),
		"AGENTHAIL_NOTION_SPACE":       os.Getenv("AGENTHAIL_NOTION_SPACE"),
		"AGENTHAIL_NOTION_USER":        os.Getenv("AGENTHAIL_NOTION_USER"),
		"AGENTHAIL_NOTION_TZ":          os.Getenv("AGENTHAIL_NOTION_TZ"),
		"AGENTHAIL_MAX_RESPONSE_BYTES": os.Getenv("AGENTHAIL_MAX_RESPONSE_BYTES"),
		"AGENTHAIL_DEBUG":              os.Getenv("AGENTHAIL_DEBUG"),
		"PYTHONPATH":                   os.Getenv("PYTHONPATH"),
		"PATH":                         envOr("PATH", "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"),
	}
	var envXML strings.Builder
	for _, key := range []string{"AGENTHAIL_SIDECAR", "AGENTHAIL_COOKIE_BRIDGE", "AGENTHAIL_PYTHON", "AGENTHAIL_CODEX_REMOTE", "AGENTHAIL_CHROME_PROFILE", "AGENTHAIL_NOTION_SPACE", "AGENTHAIL_NOTION_USER", "AGENTHAIL_NOTION_TZ", "AGENTHAIL_MAX_RESPONSE_BYTES", "AGENTHAIL_DEBUG", "PYTHONPATH", "PATH"} {
		if environment[key] != "" {
			fmt.Fprintf(&envXML, "    <key>%s</key><string>%s</string>\n", key, html.EscapeString(environment[key]))
		}
	}
	plist := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>daemon-run</string></array>
  <key>EnvironmentVariables</key><dict>
%s  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Background</string>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, daemonLaunchdLabel, html.EscapeString(exe), envXML.String(), html.EscapeString(logPath), html.EscapeString(logPath))
	path := daemonServicePath()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	if err := os.WriteFile(path, []byte(plist), 0600); err != nil {
		return fmt.Errorf("write launchd service: %w", err)
	}
	// launchd can transiently report EIO immediately after a matching service is
	// booted out. Retry after a real release window so an upgrade cannot fail
	// after its new payload has already been staged.
	if output, err := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); err != nil {
		time.Sleep(time.Second)
		if retryOutput, retryErr := exec.Command("launchctl", "bootstrap", domain, path).CombinedOutput(); retryErr != nil {
			return fmt.Errorf("bootstrap launchd service: %w (%s; retry: %s)", retryErr, strings.TrimSpace(string(output)), strings.TrimSpace(string(retryOutput)))
		}
	}
	if output, err := exec.Command("launchctl", "kickstart", "-k", domain+"/"+daemonLaunchdLabel).CombinedOutput(); err != nil {
		return fmt.Errorf("start launchd service: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if pid, running := daemon.IsRunning(); running {
			fmt.Printf("installed and started launchd service %s (pid %d)\n", daemonLaunchdLabel, pid)
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("launchd service loaded but daemon did not become ready; inspect %s", logPath)
}

func rotateLogFile(path string, maxBytes int64) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Size() <= maxBytes {
		return nil
	}
	backup := path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Rename(path, backup)
}

func (a *App) daemonUninstallService() error {
	if runtime.GOOS != "darwin" {
		return fmt.Errorf("daemon service installation currently supports macOS launchd only")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+daemonLaunchdLabel).Run()
	if err := os.Remove(daemonServicePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("uninstalled launchd service %s\n", daemonLaunchdLabel)
	return nil
}

func daemonServiceLoaded() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), daemonLaunchdLabel)
	return exec.Command("launchctl", "print", target).Run() == nil
}

func daemonServicePID() (int, bool) {
	if runtime.GOOS != "darwin" {
		return 0, false
	}
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), daemonLaunchdLabel)
	output, err := exec.Command("launchctl", "print", target).Output()
	if err != nil {
		return 0, false
	}
	return parseDaemonServicePID(string(output)), true
}

func parseDaemonServicePID(output string) int {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pid = ") {
			continue
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "pid = ")))
		if parseErr == nil && pid > 0 {
			return pid
		}
	}
	return 0
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

package cli

import (
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
)

const (
	daemonLaunchdLabel         = "com.agenthail.daemon"
	homebrewDaemonLaunchdLabel = "homebrew.mxcl.agenthail"
)

type launchdService struct{}

func (launchdService) Name() string { return "launchd" }

func (launchdService) Loaded() bool {
	return daemonServiceLoaded() || homebrewDaemonServiceLoaded()
}

func (launchdService) Check() error               { return nil }
func (launchdService) StatusLines() []string      { return nil }
func (launchdService) Problems() []serviceProblem { return nil }

func (launchdService) Start() error {
	if homebrewDaemonManaged() {
		return fmt.Errorf("daemon is managed by Homebrew; use 'brew services start agenthail'")
	}
	return startUnsupervisedDaemon()
}

func (launchdService) Stop() error {
	if homebrewDaemonManaged() {
		return fmt.Errorf("daemon is managed by Homebrew; use 'brew services stop agenthail'")
	}
	pid, ok := daemon.IsRunning()
	if !ok {
		fmt.Println("daemon not running")
		return nil
	}
	if servicePID, loaded := launchdServicePID(daemonLaunchdLabel); loaded && servicePID == pid {
		return fmt.Errorf("daemon is supervised by launchd; use 'agenthail daemon uninstall' to stop and remove the service")
	}
	if err := daemon.Stop(); err != nil {
		return err
	}
	fmt.Printf("daemon stopped (was pid %d)\n", pid)
	return nil
}

func (s launchdService) Restart() error {
	if homebrewDaemonServiceLoaded() {
		return restartLaunchdDaemon(homebrewDaemonLaunchdLabel)
	}
	if homebrewDaemonManaged() {
		return fmt.Errorf("daemon is managed by Homebrew; use 'brew services start agenthail'")
	}
	if daemonServiceLoaded() {
		return restartLaunchdDaemon(daemonLaunchdLabel)
	}
	if _, running := daemon.IsRunning(); running {
		if err := daemon.Stop(); err != nil {
			return err
		}
	}
	return s.Start()
}

// daemonServicePlist runs the daemon at the Standard launchd process type.
// Background makes macOS throttle the API server whenever the Mac is busy.
// AbandonProcessGroup keeps launchd from killing what the daemon started, the
// Claude host and its sessions, when the daemon exits or restarts.
func daemonServicePlist(exe, envXML, logPath string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>%s</string>
  <key>ProgramArguments</key><array><string>%s</string><string>daemon-run</string></array>
  <key>EnvironmentVariables</key><dict>
%s  </dict>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><true/>
  <key>ProcessType</key><string>Standard</string>
  <key>AbandonProcessGroup</key><true/>
  <key>StandardOutPath</key><string>%s</string>
  <key>StandardErrorPath</key><string>%s</string>
</dict></plist>
`, daemonLaunchdLabel, html.EscapeString(exe), envXML, html.EscapeString(logPath), html.EscapeString(logPath))
}

func daemonServicePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", daemonLaunchdLabel+".plist")
}

func (launchdService) Install() error {
	if homebrewDaemonManaged() {
		return fmt.Errorf("daemon is managed by Homebrew; use 'brew services restart agenthail'")
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
	environment := serviceEnvironment()
	var envXML strings.Builder
	for _, key := range serviceEnvironmentKeys {
		if environment[key] != "" {
			fmt.Fprintf(&envXML, "    <key>%s</key><string>%s</string>\n", key, html.EscapeString(environment[key]))
		}
	}
	plist := daemonServicePlist(exe, envXML.String(), logPath)
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
	// RunAtLoad starts the daemon as bootstrap loads it. A kickstart here races
	// that launch: launchd can abandon the half-started instance, which then runs
	// unsupervised while launchd's own copy exits on the daemon lock.
	if pid, ok := waitForSupervisedDaemon(daemonLaunchdLabel, 0, 15*time.Second); ok {
		fmt.Printf("installed and started launchd service %s (pid %d)\n", daemonLaunchdLabel, pid)
		printCodexSetupNote()
		return nil
	}
	return fmt.Errorf("launchd service loaded but its daemon did not become ready; inspect %s", logPath)
}

func (launchdService) Uninstall() error {
	if homebrewDaemonManaged() {
		return fmt.Errorf("daemon is managed by Homebrew; use 'brew services stop agenthail'")
	}
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	_ = exec.Command("launchctl", "bootout", domain+"/"+daemonLaunchdLabel).Run()
	if err := os.Remove(daemonServicePath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	fmt.Printf("uninstalled launchd service %s\n", daemonLaunchdLabel)
	return nil
}

func restartLaunchdDaemon(label string) error {
	before, _ := daemon.IsRunning()
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
	if output, err := exec.Command("launchctl", "kickstart", "-k", target).CombinedOutput(); err != nil {
		return fmt.Errorf("restart launchd service: %w (%s)", err, strings.TrimSpace(string(output)))
	}
	if pid, ok := waitForSupervisedDaemon(label, before, 15*time.Second); ok {
		fmt.Printf("daemon restarted (pid %d)\n", pid)
		return nil
	}
	return fmt.Errorf("launchd restarted the service but the daemon did not become ready")
}

func daemonServiceLoaded() bool {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), daemonLaunchdLabel)
	return exec.Command("launchctl", "print", target).Run() == nil
}

// waitForSupervisedDaemon waits for a daemon other than previous that launchd
// runs for label: the pid in the daemon's pid file must be the service's pid,
// so a daemon running outside launchd never counts as started.
func waitForSupervisedDaemon(label string, previous int, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		pid, running := daemon.IsRunning()
		if running && pid != previous {
			if servicePID, loaded := launchdServicePID(label); loaded && servicePID == pid {
				return pid, true
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, false
}

func launchdServicePID(label string) (int, bool) {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), label)
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

func homebrewDaemonServiceLoaded() bool {
	target := fmt.Sprintf("gui/%d/%s", os.Getuid(), homebrewDaemonLaunchdLabel)
	return exec.Command("launchctl", "print", target).Run() == nil
}

func homebrewDaemonManaged() bool {
	if homebrewDaemonServiceLoaded() {
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return homebrewManagedExecutable(exe)
}

func homebrewManagedExecutable(exe string) bool {
	return strings.Contains(filepath.Clean(exe), string(filepath.Separator)+"Cellar"+string(filepath.Separator)+"agenthail"+string(filepath.Separator))
}

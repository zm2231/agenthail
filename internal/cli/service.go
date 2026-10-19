package cli

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/codexconfig"
	"github.com/zm2231/agenthail/internal/daemon"
)

type daemonService interface {
	Name() string
	Loaded() bool
	Check() error
	Install() error
	Uninstall() error
	Start() error
	Stop() error
	Restart() error
	StatusLines() []string
	Problems() []serviceProblem
}

type serviceProblem struct {
	Problem string `json:"problem"`
	Detail  string `json:"detail"`
	Fix     string `json:"fix"`
}

func newDaemonService(goos string) daemonService {
	switch goos {
	case "darwin":
		return launchdService{}
	case "linux":
		return newSystemdService()
	default:
		return unsupportedService{goos: goos}
	}
}

func (a *App) service() daemonService {
	if a.daemonService == nil {
		a.daemonService = newDaemonService(runtime.GOOS)
	}
	return a.daemonService
}

var serviceEnvironmentKeys = []string{"AGENTHAIL_SIDECAR", "AGENTHAIL_COOKIE_BRIDGE", "AGENTHAIL_PYTHON", "CODEX_HOME", "CLAUDE_CONFIG_DIR", "AGENTHAIL_CLAUDE_BIN", "AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT", "AGENTHAIL_NOTION_SPACE", "AGENTHAIL_NOTION_USER", "AGENTHAIL_NOTION_TZ", "AGENTHAIL_MAX_RESPONSE_BYTES", "AGENTHAIL_DEBUG", "PYTHONPATH", "PATH"}

func serviceEnvironment() map[string]string {
	return map[string]string{
		"AGENTHAIL_SIDECAR":                     os.Getenv("AGENTHAIL_SIDECAR"),
		"AGENTHAIL_COOKIE_BRIDGE":               os.Getenv("AGENTHAIL_COOKIE_BRIDGE"),
		"AGENTHAIL_PYTHON":                      os.Getenv("AGENTHAIL_PYTHON"),
		"CODEX_HOME":                            os.Getenv("CODEX_HOME"),
		"CLAUDE_CONFIG_DIR":                     os.Getenv("CLAUDE_CONFIG_DIR"),
		"AGENTHAIL_CLAUDE_BIN":                  os.Getenv("AGENTHAIL_CLAUDE_BIN"),
		"AGENTHAIL_CODEX_REMOTE_DEBUGGING_PORT": codexconfig.RemoteDebuggingPort(),
		"AGENTHAIL_NOTION_SPACE":                os.Getenv("AGENTHAIL_NOTION_SPACE"),
		"AGENTHAIL_NOTION_USER":                 os.Getenv("AGENTHAIL_NOTION_USER"),
		"AGENTHAIL_NOTION_TZ":                   os.Getenv("AGENTHAIL_NOTION_TZ"),
		"AGENTHAIL_MAX_RESPONSE_BYTES":          os.Getenv("AGENTHAIL_MAX_RESPONSE_BYTES"),
		"AGENTHAIL_DEBUG":                       os.Getenv("AGENTHAIL_DEBUG"),
		"PYTHONPATH":                            os.Getenv("PYTHONPATH"),
		"PATH":                                  envOr("PATH", "/usr/local/bin:/opt/homebrew/bin:/usr/bin:/bin"),
	}
}

func startUnsupervisedDaemon() error {
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

func stopUnsupervisedDaemon() error {
	pid, ok := daemon.IsRunning()
	if !ok {
		fmt.Println("daemon not running")
		return nil
	}
	if err := daemon.Stop(); err != nil {
		return err
	}
	fmt.Printf("daemon stopped (was pid %d)\n", pid)
	return nil
}

func restartUnsupervisedDaemon() error {
	if _, running := daemon.IsRunning(); running {
		if err := daemon.Stop(); err != nil {
			return err
		}
	}
	return startUnsupervisedDaemon()
}

func waitForDaemonPID(previous int, timeout time.Duration) (int, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if pid, running := daemon.IsRunning(); running && pid != previous {
			return pid, true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return 0, false
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

// probeExec reports why dir cannot run executables, or "" when it can. A
// noexec or virtiofs mount accepts the file but refuses to execute it.
func probeExec(dir string) string {
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err.Error()
	}
	file, err := os.CreateTemp(dir, ".agenthail-exec-probe-")
	if err != nil {
		return err.Error()
	}
	path := file.Name()
	defer os.Remove(path)
	_, writeErr := file.WriteString("#!/bin/sh\nexit 0\n")
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr.Error()
	}
	if closeErr != nil {
		return closeErr.Error()
	}
	if err := os.Chmod(path, 0700); err != nil {
		return err.Error()
	}
	output, err := exec.Command(path).CombinedOutput()
	if err != nil {
		if message := strings.TrimSpace(string(output)); message != "" {
			return message
		}
		return err.Error()
	}
	return ""
}

func codexHomeFrom(environment map[string]string) string {
	if value := strings.TrimSpace(environment["CODEX_HOME"]); value != "" {
		return value
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".codex")
}

type unsupportedService struct{ goos string }

func (s unsupportedService) err() error {
	return fmt.Errorf("daemon service installation supports macOS launchd and Linux systemd, not %s", s.goos)
}

func (s unsupportedService) Name() string               { return "" }
func (s unsupportedService) Loaded() bool               { return false }
func (s unsupportedService) Check() error               { return nil }
func (s unsupportedService) Install() error             { return s.err() }
func (s unsupportedService) Uninstall() error           { return s.err() }
func (s unsupportedService) Start() error               { return startUnsupervisedDaemon() }
func (s unsupportedService) Stop() error                { return stopUnsupervisedDaemon() }
func (s unsupportedService) Restart() error             { return restartUnsupervisedDaemon() }
func (s unsupportedService) StatusLines() []string      { return nil }
func (s unsupportedService) Problems() []serviceProblem { return nil }

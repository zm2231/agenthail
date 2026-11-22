package daemonctl

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/poll"
)

func PidFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.pid")
}

func LogFilePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "daemon.log")
}

func IsRunning() (int, bool) {
	data, err := os.ReadFile(PidFilePath())
	if err != nil {
		return 0, false
	}
	var pid int
	fmt.Sscanf(string(data), "%d", &pid)
	if pid <= 0 {
		return 0, false
	}
	if err := exec.Command("kill", "-0", fmt.Sprintf("%d", pid)).Run(); err != nil || !processIsDaemon(pid) {
		return 0, false
	}
	return pid, true
}

func Stop() error {
	pid, ok := IsRunning()
	if !ok {
		return fmt.Errorf("daemon not running")
	}
	if err := exec.Command("kill", "-TERM", fmt.Sprintf("%d", pid)).Run(); err != nil {
		return err
	}
	if !waitForStopped(pid, 5*time.Second) {
		return fmt.Errorf("daemon pid %d did not stop within 5s", pid)
	}
	return nil
}

func MCPSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "mcp.sock")
}

// LocalSocketPath is where the daemon serves what only the person may do,
// naming each caller by its process.
func LocalSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "local.sock")
}

func DebugSocketPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agenthail", "debug.sock")
}

func LockFilePath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "daemon.lock")
}

func acquireLock() (*os.File, error) {
	file, err := os.OpenFile(LockFilePath(), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, fmt.Errorf("daemon already running or starting")
	}
	return file, nil
}

// HoldLock proves no daemon is running, and keeps one from starting until release.
func HoldLock() (release func(), err error) {
	if err := os.MkdirAll(filepath.Dir(LockFilePath()), 0700); err != nil {
		return nil, err
	}
	file, err := acquireLock()
	if err != nil {
		return nil, err
	}
	return func() { releaseLock(file) }, nil
}

func releaseLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func RemovePIDFileIfOwned(pid int) error {
	data, err := os.ReadFile(PidFilePath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	stored, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil || stored != pid {
		return nil
	}
	return os.Remove(PidFilePath())
}

func processIsDaemon(pid int) bool {
	output, err := exec.Command("ps", "-p", strconv.Itoa(pid), "-o", "command=").Output()
	if err != nil {
		return false
	}
	command := string(output)
	return strings.Contains(command, "agenthail") && strings.Contains(command, "daemon-run")
}

func waitForStopped(pid int, timeout time.Duration) bool {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		if err := syscall.Kill(pid, 0); err != nil {
			return true, nil
		}
		return false, nil
	}) == nil
}

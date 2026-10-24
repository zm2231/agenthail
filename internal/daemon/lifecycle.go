package daemon

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

func LockFilePath() string {
	return filepath.Join(filepath.Dir(PidFilePath()), "daemon.lock")
}

func acquireDaemonLock() (*os.File, error) {
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

func releaseDaemonLock(file *os.File) {
	if file == nil {
		return
	}
	_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	_ = file.Close()
}

func removePIDFileIfOwned(pid int) error {
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

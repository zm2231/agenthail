package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"syscall"

	"github.com/zm2231/agenthail/internal/daemon"
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
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("open log: %w", err)
	}
	cmd := exec.Command(exe, "daemon-run")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("spawn daemon: %w", err)
	}
	pid := cmd.Process.Pid
	if err := os.WriteFile(daemon.PidFilePath(), []byte(strconv.Itoa(pid)), 0644); err != nil {
		return err
	}
	cmd.Process.Release()
	fmt.Printf("daemon started (pid %d)\n", pid)
	fmt.Printf("log: %s\n", logPath)
	fmt.Printf("stop: agenthail daemon stop\n")
	return nil
}

func (a *App) daemonStop() error {
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

func (a *App) daemonStatus() error {
	pid, ok := daemon.IsRunning()
	if !ok {
		fmt.Println("daemon: not running")
		return nil
	}
	fmt.Printf("daemon: running (pid %d)\n", pid)
	logPath := daemon.LogFilePath()
	if info, err := os.Stat(logPath); err == nil {
		fmt.Printf("log: %s (%s)\n", logPath, humanSize(info.Size()))
	}
	return nil
}

// daemonRun is the in-process entry for the spawned daemon.
func (a *App) daemonRun() error {
	for _, e := range a.Surfaces {
		_ = e
	}
	d := daemon.New(a.Registry, a.allSurfaces())
	return d.RunWithSignal()
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

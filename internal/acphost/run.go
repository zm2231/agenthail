package acphost

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/zm2231/agenthail/internal/poll"
)

const (
	startTimeout = 15 * time.Second
	maxLogBytes  = 4 << 20
)

func StateDir(home string) string { return filepath.Join(home, ".agenthail", "acp-host") }
func lockPath(home string) string { return filepath.Join(StateDir(home), "lock") }
func LogPath(home string) string  { return filepath.Join(StateDir(home), "host.log") }

// Run serves as the host until it is drained of its last session; the daemon passes the held host lock as fd 3.
func Run(home, version string) error {
	lock := os.NewFile(3, "lock")
	held, err := lock.Stat()
	if err != nil {
		return fmt.Errorf("the ACP host needs its lock as fd 3: %w", err)
	}
	expected, err := os.Stat(lockPath(home))
	if err != nil || !os.SameFile(held, expected) {
		return fmt.Errorf("fd 3 is not %s", lockPath(home))
	}
	hostLock = lock
	logger := log.New(os.Stderr, "", log.LstdFlags)
	agents, problems := LoadAgents(home)
	for _, problem := range problems {
		logger.Printf("%v", problem)
	}
	socket := SocketPath(home)
	_ = os.Remove(socket)
	listener, err := net.Listen("unix", socket)
	if err != nil {
		return err
	}
	_ = os.Chmod(socket, 0o600)
	host := NewHost(home, version, agents, logger)
	logger.Printf("ACP host %s serving %d agents on %s", version, len(agents), socket)
	return host.Serve(listener, socket)
}

type Manager struct {
	Home    string
	Version string
	Binary  func() (string, error)
	Env     []string
	mu      sync.Mutex
}

func (m *Manager) Client() Client { return Client{Home: m.Home} }

// Ensure returns once a host of this build answers, starting one when none does and draining one of another build first.
func (m *Manager) Ensure(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := os.MkdirAll(StateDir(m.Home), 0o700); err != nil {
		return err
	}
	client := m.Client()
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		attempt, stop := context.WithTimeout(ctx, time.Second)
		hello, err := client.Hello(attempt)
		stop()
		if err == nil {
			if hello.Protocol == Protocol && hello.Version == m.Version && !hello.Draining {
				return true, nil
			}
			return false, client.Drain(ctx)
		}
		if !errors.Is(err, ErrNotRunning) {
			return false, nil
		}
		lock, err := m.lock()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return false, m.spawn(lock)
	})
}

func (m *Manager) lock() (*os.File, error) {
	file, err := os.OpenFile(lockPath(m.Home), os.O_CREATE|os.O_RDONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

func (m *Manager) spawn(lock *os.File) error {
	defer lock.Close()
	binary, err := m.Binary()
	if err != nil {
		return err
	}
	path := LogPath(m.Home)
	if info, err := os.Stat(path); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(path, path+".1")
	}
	logFile, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	cmd := exec.Command(binary, "acp-host")
	cmd.Env = m.Env
	cmd.Dir = StateDir(m.Home)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	cmd.ExtraFiles = []*os.File{lock}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start the ACP host: %w", err)
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

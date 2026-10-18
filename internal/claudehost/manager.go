package claudehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

// ErrReplacing means a host of another version is draining; its sessions
// close once idle and the next Ensure after it exits starts this version.
var ErrReplacing = errors.New("the Claude host is being replaced by this Agenthail version")

const (
	startTimeout = 15 * time.Second
	maxLogBytes  = 4 << 20
)

type Launch struct {
	Node    string
	Script  string
	Claude  string
	Version string
	Env     []string
}

// Manager starts the host for the daemon. Only the daemon calls Ensure;
// every other process reaches the host through a Client.
type Manager struct {
	Home   string
	Launch func() (Launch, error)
	mu     sync.Mutex
}

func (m *Manager) Client() Client { return NewClient(m.Home) }

func (m *Manager) stateDir() string { return filepath.Join(m.Home, ".agenthail", "claude-host") }

// Ensure returns a client for a running host of this build, starting one
// only when none answers. The host lock decides who starts it: the daemon
// takes it and hands it to the host, which holds it for its lifetime, so a
// second daemon or a restarted one attaches to the running host.
func (m *Manager) Ensure(ctx context.Context) (Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	launch, err := m.Launch()
	if err != nil {
		return Client{}, err
	}
	client := m.Client()
	if hello, err := client.Hello(ctx); err == nil {
		return client, m.current(ctx, client, hello, launch)
	} else if !errors.Is(err, ErrNotRunning) {
		return Client{}, err
	}
	if err := os.MkdirAll(m.stateDir(), 0700); err != nil {
		return Client{}, err
	}
	lock, err := m.lock()
	if errors.Is(err, syscall.EWOULDBLOCK) {
		hello, err := m.awaitHello(ctx, client, nil)
		if err != nil {
			return Client{}, fmt.Errorf("the Claude host holds its lock but does not answer: %w", err)
		}
		return client, m.current(ctx, client, hello, launch)
	}
	if err != nil {
		return Client{}, err
	}
	exited, err := m.spawn(launch, lock)
	if err != nil {
		return Client{}, err
	}
	hello, err := m.awaitHello(ctx, client, exited)
	if err != nil {
		return Client{}, fmt.Errorf("start the Claude host: %w%s", err, m.logTail())
	}
	return client, m.current(ctx, client, hello, launch)
}

func (m *Manager) current(ctx context.Context, client Client, hello Hello, launch Launch) error {
	if hello.Protocol == Protocol && hello.HostVersion == launch.Version {
		return nil
	}
	if err := client.Drain(ctx); err != nil {
		return err
	}
	return fmt.Errorf("%w (running %s protocol %d)", ErrReplacing, hello.HostVersion, hello.Protocol)
}

func (m *Manager) lock() (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(m.stateDir(), "lock"), os.O_CREATE|os.O_RDONLY, 0600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		file.Close()
		return nil, err
	}
	return file, nil
}

// spawn starts the host in its own session so a daemon exit or a signal to
// the daemon's process group never reaches it, and passes the held lock as
// fd 3. The daemon's copy of the lock is closed once the host has it.
func (m *Manager) spawn(launch Launch, lock *os.File) (<-chan error, error) {
	defer lock.Close()
	logPath := filepath.Join(m.stateDir(), "host.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(logPath, logPath+".1")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return nil, err
	}
	defer logFile.Close()
	cmd := exec.Command(launch.Node, launch.Script,
		"--socket", SocketPath(m.Home),
		"--state", m.stateDir(),
		"--claude", launch.Claude,
		"--version", launch.Version)
	cmd.Env = launch.Env
	cmd.Dir = m.stateDir()
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.ExtraFiles = []*os.File{lock}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start the Claude host: %w", err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	return exited, nil
}

func (m *Manager) awaitHello(ctx context.Context, client Client, exited <-chan error) (Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, stop := context.WithTimeout(ctx, time.Second)
		hello, err := client.Hello(attempt)
		stop()
		if err == nil {
			return hello, nil
		}
		select {
		case err := <-exited:
			return Hello{}, fmt.Errorf("the host exited: %v", err)
		case <-ctx.Done():
			return Hello{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (m *Manager) logTail() string {
	data, err := os.ReadFile(filepath.Join(m.stateDir(), "host.log"))
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 1024 {
		data = data[len(data)-1024:]
	}
	return ": " + strings.TrimSpace(string(data))
}

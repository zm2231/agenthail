package browserhost

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/zm2231/agenthail/internal/poll"
)

const (
	startTimeout = 15 * time.Second
	stopTimeout  = 5 * time.Second
	healthyRun   = time.Minute
	maxLogBytes  = 4 << 20
)

var (
	minRestart = time.Second
	maxRestart = 30 * time.Second
)

type Launch struct {
	Node    string
	Script  string
	Version string
	Env     []string
}

type Manager struct {
	Home    string
	Launch  func() (Launch, error)
	Changed func()

	mu     sync.Mutex
	status Status
}

func StateDir(home string) string { return filepath.Join(home, ".agenthail", "browser-host") }

func SocketPath(home string) string { return filepath.Join(StateDir(home), "host.sock") }

func BrowsersDir(home string) string { return filepath.Join(StateDir(home), "browsers") }

func (m *Manager) Client() Client { return Client{Socket: SocketPath(m.Home)} }

func (m *Manager) LogPath() string { return filepath.Join(StateDir(m.Home), "host.log") }

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.status.State == "" {
		return Status{State: StateUnavailable, Error: ErrNotRunning.Error()}
	}
	return m.status
}

func (m *Manager) setStatus(status Status) {
	m.mu.Lock()
	changed := !reflect.DeepEqual(m.status, status)
	m.status = status
	m.mu.Unlock()
	if changed && m.Changed != nil {
		m.Changed()
	}
}

func (m *Manager) Run(ctx context.Context) {
	wait := minRestart
	for {
		began := time.Now()
		err := m.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		m.setStatus(Status{State: StateUnavailable, Error: err.Error()})
		if time.Since(began) >= healthyRun {
			wait = minRestart
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, maxRestart)
	}
}

func (m *Manager) runOnce(ctx context.Context) error {
	launch, err := m.Launch()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(StateDir(m.Home), 0o700); err != nil {
		return err
	}
	cmd, owner, err := m.spawn(launch)
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer stop(cmd, owner, exited)
	if err := m.awaitHello(ctx, launch, cmd.Process.Pid, exited); err != nil {
		return fmt.Errorf("start the browser host: %w%s", err, m.logTail())
	}
	statusCtx, cancel := context.WithTimeout(ctx, startTimeout)
	status, err := m.Client().Status(statusCtx)
	cancel()
	if err != nil {
		return err
	}
	m.setStatus(status)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-exited:
		exited <- err
		return fmt.Errorf("the browser host exited: %v%s", err, m.logTail())
	}
}

func (m *Manager) spawn(launch Launch) (*exec.Cmd, io.WriteCloser, error) {
	logPath := m.LogPath()
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(logPath, logPath+".1")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	defer logFile.Close()
	cmd := exec.Command(launch.Node, launch.Script,
		"--socket", SocketPath(m.Home),
		"--browsers", BrowsersDir(m.Home),
		"--version", launch.Version)
	cmd.Env = launch.Env
	cmd.Dir = StateDir(m.Home)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	owner, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start the browser host: %w", err)
	}
	return cmd, owner, nil
}

func stop(cmd *exec.Cmd, owner io.Closer, exited chan error) {
	owner.Close()
	select {
	case <-exited:
	case <-time.After(stopTimeout):
		_ = cmd.Process.Kill()
		<-exited
	}
}

func (m *Manager) awaitHello(ctx context.Context, launch Launch, pid int, exited chan error) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		attempt, done := context.WithTimeout(ctx, time.Second)
		hello, err := m.Client().Hello(attempt)
		done()
		if err == nil && hello.PID == pid {
			if hello.Protocol != Protocol || hello.HostVersion != launch.Version {
				return false, fmt.Errorf("the host reports %s protocol %d, want %s protocol %d", hello.HostVersion, hello.Protocol, launch.Version, Protocol)
			}
			return true, nil
		}
		select {
		case err := <-exited:
			exited <- err
			return false, fmt.Errorf("the host exited: %v", err)
		default:
		}
		if err != nil && !errors.Is(err, ErrNotRunning) {
			return false, err
		}
		return false, nil
	})
}

func (m *Manager) logTail() string {
	data, err := os.ReadFile(m.LogPath())
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 1024 {
		data = data[len(data)-1024:]
	}
	return ": " + strings.TrimSpace(string(data))
}

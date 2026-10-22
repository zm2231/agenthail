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

// retireProtocol is the first protocol whose hosts retire when drained:
// they give up the socket and lock at once and keep serving their sessions
// elsewhere. A host of an earlier protocol is stopped instead.
const retireProtocol = 2

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
	// Agenthail is the agenthail binary the host runs as each session's
	// session-context hook; without it sessions are not told who they are.
	Agenthail string
}

// Manager starts the host for the daemon. Only the daemon calls Ensure;
// every other process reaches the host through a Client.
type Manager struct {
	Home   string
	Launch func() (Launch, error)
	mu     sync.Mutex
}

func (m *Manager) Client() Client { return NewClient(m.Home) }

func (m *Manager) stateDir() string { return StateDir(m.Home) }

// LogPath is the host's stdout and stderr.
func (m *Manager) LogPath() string { return filepath.Join(m.stateDir(), "host.log") }

// StateDir holds the host's lock, log and state.json, the record of the Claude
// processes it started, which outlives the host.
func StateDir(home string) string { return filepath.Join(home, ".agenthail", "claude-host") }

// Ensure returns a client for a running host of this build, starting one
// only when none answers. The host lock decides who starts it: the daemon
// takes it and hands it to the host, which holds it until it exits or
// retires, so a second daemon or a restarted one attaches to the running
// host. A daemon that finds the lock taken keeps trying it, so it starts the
// host itself if the holder exits before its host answers. A host of another
// version is retired first and this build's host started in its place.
func (m *Manager) Ensure(ctx context.Context) (Client, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	launch, err := m.Launch()
	if err != nil {
		return Client{}, err
	}
	client := m.Client()
	if err := os.MkdirAll(m.stateDir(), 0700); err != nil {
		return Client{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, stop := context.WithTimeout(ctx, time.Second)
		hello, err := client.Hello(attempt)
		stop()
		if err == nil {
			if current(hello, launch) {
				return client, nil
			}
			if err := retire(ctx, client, hello); err != nil {
				return Client{}, err
			}
			continue
		}
		if !errors.Is(err, ErrNotRunning) {
			return Client{}, err
		}
		lock, err := m.lock()
		if err == nil {
			return m.start(ctx, client, launch, lock)
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return Client{}, err
		}
		lockTaken()
		select {
		case <-ctx.Done():
			return Client{}, fmt.Errorf("the Claude host holds its lock but does not answer: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// lockTaken runs each time Ensure finds the host lock held by another
// process; tests use it to release the lock only once Ensure has seen it held.
var lockTaken = func() {}

func (m *Manager) start(ctx context.Context, client Client, launch Launch, lock *os.File) (Client, error) {
	exited, err := m.spawn(launch, lock)
	if err != nil {
		return Client{}, err
	}
	hello, err := m.awaitHello(ctx, client, exited)
	if err != nil {
		return Client{}, fmt.Errorf("start the Claude host: %w%s", err, m.logTail())
	}
	if !current(hello, launch) {
		return Client{}, fmt.Errorf("the started Claude host reports %s protocol %d, want %s protocol %d", hello.HostVersion, hello.Protocol, launch.Version, Protocol)
	}
	return client, nil
}

func current(hello Hello, launch Launch) bool {
	return hello.Protocol == Protocol && hello.HostVersion == launch.Version
}

// retire moves a host of another version out of the way. One that retires
// answers drain only after it has left the socket and released the lock, and
// keeps serving its sessions on a socket of its own until each one closes. An
// older host cannot do that and is stopped: the Claude processes it started
// run on until they exit, held from a new host meanwhile, and the next
// message after that resumes the conversation on this build's host.
func retire(ctx context.Context, client Client, hello Hello) error {
	if hello.Protocol >= retireProtocol {
		return client.Drain(ctx)
	}
	if hello.PID <= 0 {
		return fmt.Errorf("the Claude host %s protocol %d named no pid to stop", hello.HostVersion, hello.Protocol)
	}
	if err := syscall.Kill(hello.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("stop the Claude host %s (pid %d): %w", hello.HostVersion, hello.PID, err)
	}
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		attempt, stop := context.WithTimeout(ctx, time.Second)
		again, err := client.Hello(attempt)
		stop()
		if errors.Is(err, ErrNotRunning) || err == nil && again.PID != hello.PID {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("the Claude host %s (pid %d) did not stop: %w", hello.HostVersion, hello.PID, ctx.Err())
		case <-ticker.C:
		}
	}
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
	logPath := m.LogPath()
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
	if launch.Agenthail != "" {
		cmd.Args = append(cmd.Args, "--agenthail", launch.Agenthail)
	}
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
	data, err := os.ReadFile(m.LogPath())
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 1024 {
		data = data[len(data)-1024:]
	}
	return ": " + strings.TrimSpace(string(data))
}

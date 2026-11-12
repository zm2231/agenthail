// Package nodehost supervises a Node process the daemon runs beside itself
// and talks to it over a private Unix socket.
package nodehost

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
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

type Launch struct {
	Node    string
	Script  string
	Version string
	Env     []string
}

type Supervisor struct {
	Name     string
	StateDir string
	Protocol int
	Launch   func() (Launch, error)
	Args func() []string
	Ready func(ctx context.Context, client Client) error
	Down func(err error)

	MinRestart time.Duration
	MaxRestart time.Duration

	mu     sync.Mutex
	secret string
}

func (s *Supervisor) SocketPath() string { return filepath.Join(s.StateDir, "host.sock") }

func (s *Supervisor) LogPath() string { return filepath.Join(s.StateDir, "host.log") }

func (s *Supervisor) Client() Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Client{Name: s.Name, Socket: s.SocketPath(), Secret: s.secret}
}

func (s *Supervisor) Run(ctx context.Context) {
	minimum, maximum := s.MinRestart, s.MaxRestart
	if minimum <= 0 {
		minimum = time.Second
	}
	if maximum <= 0 {
		maximum = 30 * time.Second
	}
	wait := minimum
	for {
		began := time.Now()
		err := s.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if s.Down != nil {
			s.Down(err)
		}
		if time.Since(began) >= healthyRun {
			wait = minimum
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, maximum)
	}
}

func (s *Supervisor) runOnce(ctx context.Context) error {
	launch, err := s.Launch()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(s.StateDir, 0o700); err != nil {
		return err
	}
	cmd, owner, err := s.spawn(launch)
	if err != nil {
		return err
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	defer stop(cmd, owner, exited)
	if err := s.awaitHello(ctx, launch, cmd.Process.Pid, exited); err != nil {
		return fmt.Errorf("start the %s: %w%s", s.Name, err, s.logTail())
	}
	if s.Ready != nil {
		readyCtx, cancel := context.WithTimeout(ctx, startTimeout)
		err := s.Ready(readyCtx, s.Client())
		cancel()
		if err != nil {
			return err
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-exited:
		exited <- err
		return fmt.Errorf("the %s exited: %v%s", s.Name, err, s.logTail())
	}
}

func (s *Supervisor) spawn(launch Launch) (*exec.Cmd, io.WriteCloser, error) {
	logPath := s.LogPath()
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(logPath, logPath+".1")
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, err
	}
	defer logFile.Close()
	args := []string{launch.Script, "--socket", s.SocketPath(), "--version", launch.Version}
	if s.Args != nil {
		args = append(args, s.Args()...)
	}
	cmd := exec.Command(launch.Node, args...)
	cmd.Env = launch.Env
	cmd.Dir = s.StateDir
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	owner, err := cmd.StdinPipe()
	if err != nil {
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, nil, fmt.Errorf("start the %s: %w", s.Name, err)
	}
	secret := rand.Text() + rand.Text()
	s.mu.Lock()
	s.secret = secret
	s.mu.Unlock()
	if _, err := io.WriteString(owner, secret+"\n"); err != nil {
		owner.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, nil, fmt.Errorf("hand the %s its secret: %w", s.Name, err)
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

func (s *Supervisor) awaitHello(ctx context.Context, launch Launch, pid int, exited chan error) error {
	ctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		attempt, done := context.WithTimeout(ctx, time.Second)
		hello, err := s.Client().Hello(attempt)
		done()
		if err == nil && hello.PID == pid {
			if hello.Protocol != s.Protocol || hello.HostVersion != launch.Version {
				return false, fmt.Errorf("the host reports %s protocol %d, want %s protocol %d", hello.HostVersion, hello.Protocol, launch.Version, s.Protocol)
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

func (s *Supervisor) logTail() string {
	data, err := os.ReadFile(s.LogPath())
	if err != nil || len(data) == 0 {
		return ""
	}
	if len(data) > 1024 {
		data = data[len(data)-1024:]
	}
	return ": " + strings.TrimSpace(string(data))
}

package triggers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateTimedOut  = "timed_out"

	OutputTailBytes = 8 << 10
	killGrace       = 5 * time.Second
)

type Result struct {
	State    string
	ExitCode int
	Signal   string
	Stdout   string
	Stderr   string
	Error    string
}

type Runner struct {
	WorkDir string
	Grace   time.Duration
}

func (r Runner) Execute(ctx context.Context, trigger Trigger, c Completion, test bool) Result {
	if trigger.Touch != "" {
		if err := touch(trigger.Touch); err != nil {
			return Result{State: StateFailed, ExitCode: -1, Error: err.Error()}
		}
		return Result{State: StateSucceeded}
	}
	return r.run(ctx, trigger, c, test)
}

func (r Runner) run(ctx context.Context, trigger Trigger, c Completion, test bool) Result {
	if err := safeExecutable(trigger.Run[0]); err != nil {
		return Result{State: StateFailed, ExitCode: -1, Error: err.Error()}
	}
	stdout, stderr := &tail{limit: OutputTailBytes}, &tail{limit: OutputTailBytes}
	cmd := exec.Command(trigger.Run[0], trigger.Run[1:]...)
	cmd.Dir = r.WorkDir
	cmd.Env = environment(trigger, c)
	cmd.Stdin = bytes.NewReader(Payload(c, trigger.Name, test))
	cmd.Stdout, cmd.Stderr = stdout, stderr
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		return Result{State: StateFailed, ExitCode: -1, Error: err.Error()}
	}
	group := cmd.Process.Pid
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	timeout := trigger.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	timedOut := false
	var err error
	select {
	case err = <-waited:
	case <-timer.C:
		timedOut = true
		err = r.stop(group, waited)
	case <-ctx.Done():
		err = r.stop(group, waited)
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	var exit *exec.ExitError
	switch {
	case timedOut:
		result.State, result.ExitCode, result.Error = StateTimedOut, -1, fmt.Sprintf("ran past %s", timeout)
	case err == nil:
		result.State = StateSucceeded
	case errors.As(err, &exit):
		result.State, result.ExitCode = StateFailed, exit.ExitCode()
		if status, ok := exit.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			result.Signal = status.Signal().String()
		}
	default:
		result.State, result.ExitCode, result.Error = StateFailed, -1, err.Error()
	}
	if ctx.Err() != nil && !timedOut {
		result.Error = "the daemon stopped while it ran"
	}
	return result
}

// stop ends the run's whole process group: SIGTERM, then SIGKILL once the
// grace passes, so a child that ignores SIGTERM cannot outlive the timeout.
func (r Runner) stop(group int, waited <-chan error) error {
	_ = syscall.Kill(-group, syscall.SIGTERM)
	grace := r.Grace
	if grace <= 0 {
		grace = killGrace
	}
	select {
	case err := <-waited:
		return err
	case <-time.After(grace):
	}
	_ = syscall.Kill(-group, syscall.SIGKILL)
	return <-waited
}

func environment(trigger Trigger, c Completion) []string {
	env := []string{}
	for _, name := range []string{"PATH", "HOME", "LANG", "TZ"} {
		if value, ok := os.LookupEnv(name); ok {
			env = append(env, name+"="+value)
		}
	}
	for name, value := range trigger.Env {
		env = append(env, name+"="+value)
	}
	return append(env,
		"AGENTHAIL_TRIGGER="+trigger.Name,
		"AGENTHAIL_EVENT=turn.completed",
		"AGENTHAIL_DEDUPE_KEY="+c.DedupeKey(),
		"AGENTHAIL_SESSION_ID="+c.SessionID,
		"AGENTHAIL_TURN_ID="+c.TurnID,
	)
}

// touch never follows a symlink at the final path, and never blocks on or
// writes to anything but a regular file.
func touch(path string) error {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("touch %s: %w", path, err)
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return fmt.Errorf("touch %s: %w", path, err)
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("touch %s: not a regular file", path)
	}
	now := unix.NsecToTimeval(time.Now().UnixNano())
	if err := unix.Futimes(fd, []unix.Timeval{now, now}); err != nil {
		return fmt.Errorf("touch %s: %w", path, err)
	}
	return nil
}

type tail struct {
	mu    sync.Mutex
	limit int
	data  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.data = append(t.data, p...)
	if extra := len(t.data) - t.limit; extra > 0 {
		t.data = append(t.data[:0], t.data[extra:]...)
	}
	return len(p), nil
}

func (t *tail) String() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return string(bytes.ToValidUTF8(t.data, []byte("�")))
}

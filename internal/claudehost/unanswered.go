package claudehost

import (
	"context"
	"encoding/json"
	"errors"
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

// Any hello answered within this window resets it, so a slow host is kept.
var (
	unansweredAfter = 10 * time.Second
	retireWait      = 10 * time.Second
)

const killWait = 5 * time.Second

var errUnanswered = errors.New("the Claude host holds its socket but has not answered")

func unanswered(err error) bool {
	return errors.Is(err, os.ErrDeadlineExceeded) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, syscall.EAGAIN)
}

// SIGHUP retires the host as an update would; its sessions keep running.
func (m *Manager) replaceUnanswered(ctx context.Context) (int, error) {
	pid, err := m.currentHostPID(ctx)
	if err != nil {
		return 0, err
	}
	if err := syscall.Kill(pid, syscall.SIGHUP); err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return pid, nil
		}
		return pid, fmt.Errorf("ask the Claude host (pid %d) to retire: %w", pid, err)
	}
	if m.awaitRelease(ctx, pid, retireWait) {
		return pid, nil
	}
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return pid, fmt.Errorf("stop the Claude host (pid %d): %w", pid, err)
	}
	if m.awaitRelease(ctx, pid, killWait) {
		return pid, nil
	}
	return pid, fmt.Errorf("the Claude host (pid %d) neither retired nor exited", pid)
}

func (m *Manager) awaitRelease(ctx context.Context, pid int, wait time.Duration) bool {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	return poll.Until(ctx, 50*time.Millisecond, func() (bool, error) {
		if syscall.Kill(pid, 0) != nil {
			return true, nil
		}
		lock, err := m.lock()
		if err != nil {
			return false, nil
		}
		lock.Close()
		return true, nil
	}) == nil
}

func (m *Manager) currentHostPID(ctx context.Context) (int, error) {
	data, err := os.ReadFile(filepath.Join(m.stateDir(), "state.json"))
	if err != nil {
		return 0, fmt.Errorf("%w, and its pid is unknown: %v", errUnanswered, err)
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil || state.PID <= 0 {
		return 0, fmt.Errorf("%w, and state.json names no pid", errUnanswered)
	}
	socket := SocketPath(m.Home)
	if _, err := os.Lstat(strings.TrimSuffix(socket, ".sock") + "." + strconv.Itoa(state.PID) + ".sock"); err == nil {
		return 0, fmt.Errorf("%w, and pid %d in state.json has retired", errUnanswered, state.PID)
	}
	out, err := exec.CommandContext(ctx, "ps", "-ww", "-o", "args=", "-p", strconv.Itoa(state.PID)).Output()
	if err != nil || !strings.Contains(string(out), "--socket "+socket+" ") {
		return 0, fmt.Errorf("%w, and pid %d in state.json is not a Claude host of %s", errUnanswered, state.PID, socket)
	}
	return state.PID, nil
}

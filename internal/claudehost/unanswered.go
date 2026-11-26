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
	"github.com/zm2231/agenthail/internal/procstart"
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
	host, err := m.currentHost(ctx)
	if err != nil {
		return 0, err
	}
	signalled, err := m.signalHost(ctx, host, syscall.SIGHUP)
	if err != nil {
		return host.pid, fmt.Errorf("%w; stop pid %d to start a new one: %v", errUnanswered, host.pid, err)
	}
	if !signalled || m.awaitRelease(ctx, host.pid, retireWait) {
		return host.pid, nil
	}
	if signalled, _ := m.signalHost(ctx, host, syscall.SIGKILL); !signalled {
		return host.pid, nil
	}
	if m.awaitRelease(ctx, host.pid, killWait) {
		return host.pid, nil
	}
	return host.pid, fmt.Errorf("the Claude host (pid %d) neither retired nor exited", host.pid)
}

type hostProcess struct {
	pid     int
	started string
}

// The handle is opened before the identity check, so a later owner of the pid is never signalled.
func (m *Manager) signalHost(ctx context.Context, host hostProcess, signal syscall.Signal) (bool, error) {
	handle, err := openProcess(host.pid)
	if err != nil {
		if errors.Is(err, syscall.ESRCH) {
			return false, nil
		}
		return false, err
	}
	defer handle.close()
	again, err := m.currentHost(ctx)
	if err != nil || again != host {
		return false, nil
	}
	return handle.signal(signal) == nil, nil
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

// A host writes state.json only once it listens, so a host still starting is never named.
func (m *Manager) currentHost(ctx context.Context) (hostProcess, error) {
	data, err := os.ReadFile(filepath.Join(m.stateDir(), "state.json"))
	if err != nil {
		return hostProcess{}, fmt.Errorf("%w, and its pid is unknown: %v", errUnanswered, err)
	}
	var state struct {
		PID int `json:"pid"`
	}
	if err := json.Unmarshal(data, &state); err != nil || state.PID <= 0 {
		return hostProcess{}, fmt.Errorf("%w, and state.json names no pid", errUnanswered)
	}
	socket := SocketPath(m.Home)
	if _, err := os.Lstat(strings.TrimSuffix(socket, ".sock") + "." + strconv.Itoa(state.PID) + ".sock"); err == nil {
		return hostProcess{}, fmt.Errorf("%w, and pid %d in state.json has retired", errUnanswered, state.PID)
	}
	started, err := procstart.Lstart(ctx, state.PID)
	if err != nil || started == "" {
		return hostProcess{}, fmt.Errorf("%w, and pid %d in state.json no longer runs", errUnanswered, state.PID)
	}
	out, err := exec.CommandContext(ctx, "ps", "-ww", "-o", "args=", "-p", strconv.Itoa(state.PID)).Output()
	if err != nil || !strings.Contains(string(out), "--socket "+socket+" ") {
		return hostProcess{}, fmt.Errorf("%w, and pid %d in state.json is not a Claude host of %s", errUnanswered, state.PID, socket)
	}
	return hostProcess{pid: state.PID, started: started}, nil
}

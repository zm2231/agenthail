package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/zm2231/agenthail/internal/daemon"
	"github.com/zm2231/agenthail/internal/poll"
)

// A live process may be migrating a large registry, so it gets far longer than a dead one.
var (
	daemonStartBound = 3 * time.Minute
	daemonGoneBound  = 15 * time.Second
)

const daemonCrashLoopPIDs = 3

// daemonStart is one wait for a daemon that a service manager, or this command, just started.
type daemonStart struct {
	previous   int
	process    func() int
	supervised bool
	goneBound  time.Duration
}

// wait returns the daemon's pid once it runs and its dashboard, when enabled, answers.
func (s daemonStart) wait() (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), daemonStartBound)
	defer cancel()
	config, configErr := daemon.LoadDashboardConfig()
	wantDashboard := configErr == nil && config.Enabled
	seen := map[int]bool{}
	var gone time.Duration
	last := time.Now()
	ready := 0
	errExiting := errors.New("exiting")
	err := poll.Until(ctx, 100*time.Millisecond, func() (bool, error) {
		now := time.Now()
		current := s.process()
		if pid, running := daemon.IsRunning(); running && pid != s.previous && (!s.supervised || current == pid) {
			if !wantDashboard || dashboardAnswers(config.Listen) {
				ready = pid
				return true, nil
			}
		}
		if current == 0 {
			gone += now.Sub(last)
		} else if current != s.previous {
			seen[current] = true
		}
		last = now
		if (current == 0 && gone >= s.goneBound) || len(seen) >= daemonCrashLoopPIDs {
			return false, errExiting
		}
		return false, nil
	})
	switch {
	case err == nil:
		return ready, nil
	case errors.Is(err, errExiting):
		return 0, fmt.Errorf("the daemon keeps exiting instead of starting; inspect %s", daemonStatusLogPath())
	default:
		return 0, fmt.Errorf("the daemon did not become ready within %s; inspect %s", daemonStartBound, daemonStatusLogPath())
	}
}

// The pid file must name launchd's own pid, so a daemon outside launchd never counts as started.
func launchdDaemonStart(label string, previous int) daemonStart {
	return daemonStart{
		previous:   previous,
		supervised: true,
		goneBound:  daemonGoneBound,
		process: func() int {
			job, loaded := launchdServiceJob(label)
			if !loaded {
				return 0
			}
			return job.pid
		},
	}
}

func (s *systemdService) daemonStart(previous int) daemonStart {
	return daemonStart{
		previous:  previous,
		goneBound: daemonGoneBound,
		process: func() int {
			output, err := s.systemctl("show", systemdUnitName, "--property=MainPID", "--value")
			if err != nil {
				return 0
			}
			pid, _ := strconv.Atoi(output)
			return pid
		},
	}
}

// Nothing respawns a daemon this command started itself, so its exit ends the wait.
func spawnedDaemonStart(pid int, exited func() bool) daemonStart {
	return daemonStart{
		supervised: true,
		process: func() int {
			if exited() {
				return 0
			}
			return pid
		},
	}
}

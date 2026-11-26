package claudehost

import (
	"errors"
	"context"
	"syscall"
	"time"
)

// OnLockTaken replaces the hook Ensure runs when it finds the host lock held.
func OnLockTaken(hook func()) (restore func()) {
	previous := lockTaken
	lockTaken = hook
	return func() { lockTaken = previous }
}

func SetUnansweredWaits(after, retire time.Duration) (restore func()) {
	previousAfter, previousRetire := unansweredAfter, retireWait
	unansweredAfter, retireWait = after, retire
	return func() { unansweredAfter, retireWait = previousAfter, previousRetire }
}

func SignalHost(m *Manager, pid int, started string, signal syscall.Signal) (bool, error) {
	return m.signalHost(context.Background(), hostProcess{pid: pid, started: started}, signal)
}

func FailHandleOpensAfter(opens int) (restore func()) {
	previous := openHandle
	count := 0
	openHandle = func(pid int) (processHandle, error) {
		count++
		if count > opens {
			return processHandle{}, errors.New("no handle")
		}
		return previous(pid)
	}
	return func() { openHandle = previous }
}

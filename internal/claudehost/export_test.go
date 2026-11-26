package claudehost

import (
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

func SignalHost(m *Manager, pid int, started string, signal syscall.Signal) bool {
	return m.signalHost(context.Background(), hostProcess{pid: pid, started: started}, signal)
}

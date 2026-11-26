package claudehost

import "time"

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

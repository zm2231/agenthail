package claudehost

// OnLockTaken replaces the hook Ensure runs when it finds the host lock held.
func OnLockTaken(hook func()) (restore func()) {
	previous := lockTaken
	lockTaken = hook
	return func() { lockTaken = previous }
}

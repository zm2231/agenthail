package daemon

// Keep in step with native/SnapshotRefreshPolicy.swift.
func changesSnapshot(eventType string) bool {
	switch eventType {
	case "state.changed", "settings.updated", "session.updated", "turn.completed", "signin.changed", "host.paired", "host.removed":
		return true
	}
	return false
}

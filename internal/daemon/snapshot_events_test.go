package daemon

import "testing"

func TestEventsThatChangeTheSnapshotDropItsCache(t *testing.T) {
	d, _, _, _, _, _ := baseFixture(t)
	for _, eventType := range []string{"state.changed", "settings.updated", "session.updated", "turn.completed", "signin.changed", "host.paired", "host.removed"} {
		before := d.dashboard.stateVersion.Load()
		d.publishEvent(eventType, "", nil)
		if d.dashboard.stateVersion.Load() == before {
			t.Fatalf("%s left the cached snapshot in place", eventType)
		}
	}
	for _, eventType := range []string{"ledger.gate", "task.settled", "ui.changed"} {
		before := d.dashboard.stateVersion.Load()
		d.publishEvent(eventType, "", nil)
		if d.dashboard.stateVersion.Load() != before {
			t.Fatalf("%s dropped the cached snapshot", eventType)
		}
	}
}

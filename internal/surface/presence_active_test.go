package surface

import (
	"testing"
	"time"
)

func TestRecentlyActiveKeepsBusyAndRecentOnly(t *testing.T) {
	now := time.Now()
	for _, test := range []struct {
		name    string
		session Session
		want    bool
	}{
		{name: "busy old", session: Session{ID: "busy", Status: StatusBusy, LastActive: now.Add(-30 * 24 * time.Hour)}, want: true},
		{name: "recent idle", session: Session{ID: "recent", Status: StatusIdle, LastActive: now.Add(-time.Hour)}, want: true},
		{name: "old idle", session: Session{ID: "old", Status: StatusIdle, LastActive: now.Add(-25 * time.Hour)}},
		{name: "unknown timestamp", session: Session{ID: "unknown", Status: StatusUnknown}},
		{name: "offline", session: Session{ID: "offline", Status: StatusOffline, LastActive: now}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := RecentlyActive(test.session, now); got != test.want {
				t.Fatalf("active=%v want=%v", got, test.want)
			}
		})
	}
}

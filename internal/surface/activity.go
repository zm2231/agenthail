package surface

import (
	"time"
)

// AdvanceBusyActivity dates a working session's last activity by its
// transcript's last write. A provider dates its own status record only when
// the status changes, so a long turn would otherwise keep the time it
// started.
func AdvanceBusyActivity(session *Session, modified time.Time) {
	if session.Status == StatusBusy && modified.After(session.LastActive) {
		session.LastActive = modified
	}
}

package surfaces

import (
	"time"

	"github.com/zm2231/agenthail/internal/surface"
)

// advanceBusyActivity dates a working session's last activity by its
// transcript's last write. A provider dates its own status record only when
// the status changes, so a long turn would otherwise keep the time it
// started.
func advanceBusyActivity(session *surface.Session, modified time.Time) {
	if session.Status == surface.StatusBusy && modified.After(session.LastActive) {
		session.LastActive = modified
	}
}

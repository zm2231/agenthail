package surfaces

import (
	"context"
	"fmt"

	"github.com/zm2231/agenthail/internal/surface"
)

func (c *Claude) LocalStatusFiles(session surface.Session) []string {
	var files []string
	for _, holder := range session.Holders {
		files = append(files, c.peerRecordPath(holder.PID))
	}
	if session.PID > 0 {
		files = append(files, c.peerRecordPath(session.PID))
	}
	if len(files) == 0 {
		return nil
	}
	if session.HasLocal && session.Transcript != "" {
		files = append(files, session.Transcript)
	}
	return files
}

func (c *Claude) LocalStatus(ctx context.Context, session surface.Session) (surface.Session, error) {
	if len(session.Holders) > 0 {
		return c.sharedLocalStatus(ctx, session)
	}
	if session.PID <= 0 {
		return session, fmt.Errorf("claude session %s has no peer process", session.ID)
	}
	record, live := c.liveRecord(session)
	if !live {
		return session, fmt.Errorf("claude pid %d no longer runs session %s", session.PID, session.ID)
	}
	c.applyRecordStatus(ctx, &session, record)
	return session, nil
}

func claudeStatus(status surface.SessionStatus) surface.SessionStatus {
	return surface.NormalizeStatus(string(status))
}

// claudePeerStatus reads a Claude session record's status. Claude writes
// "shell" when its prompt is ready but a background shell is still running,
// and "waiting" while the turn is blocked on the user.
func claudePeerStatus(record map[string]any) surface.SessionStatus {
	status := surface.NormalizeStatus(str(record, "status"))
	if status == surface.StatusBusy {
		return surface.StatusBusy
	}
	if status != surface.StatusIdle {
		return surface.StatusUnknown
	}
	features, _ := record["peerFeatures"].([]any)
	for _, feature := range features {
		if feature == "notify_idle" {
			return surface.StatusIdle
		}
	}
	return surface.StatusUnknown
}

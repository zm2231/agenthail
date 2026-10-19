package surfaces

import (
	"context"
	"encoding/json"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

// Presence counts a Claude session current only while its process is open,
// unless messages are queued for it.
func (c *Claude) Presence(sess surface.Session, input surface.PresenceInput) (bool, string) {
	if input.QueueCount > 0 {
		return true, "queued"
	}
	if !input.Open {
		return false, ""
	}
	if sess.Status == surface.StatusBusy {
		return true, "working"
	}
	return true, "open"
}

// OpenSessions reports the sessions the host lists and the CLI sessions whose
// process still runs them.
func (c *Claude) OpenSessions(ctx context.Context, sessions []surface.Session) map[string]bool {
	open := map[string]bool{}
	hosted := c.host.listing(ctx)
	for _, session := range sessions {
		if claudeHosted(&session) {
			if _, ok := hosted[session.ID]; ok {
				open[session.ID] = true
			}
			continue
		}
		if _, live := c.liveRecord(session); live {
			open[session.ID] = true
		}
	}
	return open
}

func (c *Claude) readPeerRecord(pid int) (map[string]any, error) {
	raw, err := os.ReadFile(c.peerRecordPath(pid))
	if err != nil {
		return nil, err
	}
	var record map[string]any
	if err := json.Unmarshal(raw, &record); err != nil {
		return nil, err
	}
	return record, nil
}

// liveRecord reads the session record of sess's process while that process
// is still the one that wrote it for sess.
func (c *Claude) liveRecord(sess surface.Session) (map[string]any, bool) {
	if sess.PID <= 0 {
		return nil, false
	}
	record, err := c.readPeerRecord(sess.PID)
	if err != nil || str(record, "sessionId") != sess.ID || !claudeRecordLive(record) {
		return nil, false
	}
	return record, true
}

// claudeRecordLive reports whether the record's process is running and is the
// one that wrote it. Claude writes procStart as Linux start ticks from
// /proc/<pid>/stat, or as "ps -o lstart" text elsewhere.
func claudeRecordLive(record map[string]any) bool {
	pid, _ := record["pid"].(float64)
	if pid <= 0 || !claudeProcessAlive(int(pid)) {
		return false
	}
	started := str(record, "procStart")
	if started == "" {
		return true
	}
	if isClockTicks(started) {
		actual, err := procStartTicks(int(pid))
		return err == nil && actual == started
	}
	actual, err := processStartTime(int(pid))
	return err == nil && sameClaudeProcessStart(str(record, "version"), started, actual.UTC().Format(claudeProcessStartLayout))
}

package surfaces

import (
	"context"
	"time"

	"github.com/zm2231/agenthail/internal/claudepeer"
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

// liveRecord reads the session record of sess's process while that process
// is still the one that wrote it for sess.
func (c *Claude) liveRecord(sess surface.Session) (map[string]any, bool) {
	if sess.PID <= 0 {
		return c.sharedLiveRecord(sess)
	}
	_, record, found := c.pidRecord(sess.PID)
	if !found || str(record, "sessionId") != sess.ID || !claudeRecordLive(record) {
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
		return claudeRecordPredatesProcess(record, int(pid))
	}
	if claudepeer.IsClockTicks(started) {
		actual, err := claudepeer.ProcStartTicks(int(pid))
		return err == nil && actual == started
	}
	actual, err := processStartTime(int(pid))
	return err == nil && sameClaudeProcessStart(str(record, "version"), started, actual.UTC().Format(claudeProcessStartLayout))
}

// claudeRecordPredatesProcess dates a record that names no procStart by its
// startedAt, which Claude writes once its process runs, so a process that
// started after it holds a reused pid. A record with neither cannot be dated.
// startedAt is whole milliseconds, so the process start is compared to the millisecond.
func claudeRecordPredatesProcess(record map[string]any, pid int) bool {
	at, ok := record["startedAt"].(float64)
	if !ok || at <= 0 {
		return true
	}
	started, err := processStartedAt(pid)
	return err == nil && !started.Truncate(time.Millisecond).After(time.UnixMilli(int64(at)))
}

package surfaces

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strconv"
	"strings"

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

func (c *Claude) OpenSessions(_ context.Context, sessions []surface.Session) map[string]bool {
	open := map[string]bool{}
	for _, session := range sessions {
		if session.PID <= 0 {
			continue
		}
		record, err := c.readPeerRecord(session.PID)
		if err == nil && str(record, "sessionId") == session.ID && claudeRecordLive(record) {
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

// Claude writes procStart as Linux start ticks from /proc/<pid>/stat, or as
// UTC "ps -o lstart" text elsewhere.
func claudeRecordLive(record map[string]any) bool {
	pid, _ := record["pid"].(float64)
	if pid <= 0 || !claudeProcessAlive(int(pid)) {
		return false
	}
	recorded := str(record, "procStart")
	if recorded == "" {
		return true
	}
	actual, ok := claudeProcessStart(int(pid), recorded)
	return ok && actual == strings.Join(strings.Fields(recorded), " ")
}

func claudeProcessStart(pid int, recorded string) (string, bool) {
	if _, err := strconv.ParseUint(recorded, 10, 64); err == nil {
		raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return "", false
		}
		return linuxProcessStartTicks(string(raw))
	}
	command := exec.Command("ps", "-o", "lstart=", "-p", strconv.Itoa(pid))
	command.Env = append(os.Environ(), "LC_ALL=C", "TZ=UTC")
	output, err := command.Output()
	if err != nil {
		return "", false
	}
	return strings.Join(strings.Fields(string(output)), " "), true
}

// The command name in /proc/<pid>/stat may contain spaces and parentheses.
func linuxProcessStartTicks(stat string) (string, bool) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return "", false
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) < 20 {
		return "", false
	}
	return fields[19], true
}

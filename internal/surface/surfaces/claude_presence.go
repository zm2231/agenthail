package surfaces

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

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

// OpenSessions reports the sessions whose pid is a running claude process.
func (c *Claude) OpenSessions(ctx context.Context, sessions []surface.Session) map[string]bool {
	processCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	output, err := exec.CommandContext(processCtx, "ps", "-axo", "pid=,comm=").Output()
	if err != nil {
		return nil
	}
	processes := parseClaudeOpenProcesses(string(output))
	open := map[string]bool{}
	for _, session := range sessions {
		if processes[session.PID] {
			open[session.ID] = true
		}
	}
	return open
}

func parseClaudeOpenProcesses(output string) map[int]bool {
	result := map[int]bool{}
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pid, err := strconv.Atoi(fields[0])
		if err == nil && pid > 0 && filepath.Base(strings.Join(fields[1:], " ")) == "claude" {
			result[pid] = true
		}
	}
	return result
}

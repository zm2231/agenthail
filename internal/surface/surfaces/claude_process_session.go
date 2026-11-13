package surfaces

import (
	"strings"

	"github.com/zm2231/agenthail/internal/claudehost"
)

// ProcessSession names the session the Claude process pid holds now. Claude
// rewrites its record in place on /clear and /resume, so the answer is read
// fresh each time rather than remembered.
func (c *Claude) ProcessSession(pid int) string {
	_, record, found := c.pidRecord(pid)
	if !found || str(record, "agenthail") != "" || !claudeRecordLive(record) {
		return ""
	}
	if recorded, _ := record["pid"].(float64); int(recorded) != pid {
		return ""
	}
	id := str(record, "sessionId")
	if strings.HasPrefix(id, claudehost.ListingSessionPrefix) {
		return ""
	}
	return id
}

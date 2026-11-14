package surface

import (
	"strconv"
	"strings"
	"time"

	"github.com/tidwall/gjson"
)

// ClaudeDefaultHoldDeadline is how long Claude Code 2.1.325 keeps a held
// cross-session message when nothing configures it.
const ClaudeDefaultHoldDeadline = 5 * time.Minute

var claudeDialogExpiries = map[string]time.Duration{"60s": time.Minute, "5m": 5 * time.Minute, "10m": 10 * time.Minute, "never": 0}

// ClaudeHoldDeadline is how long Claude keeps a held cross-session message
// before dropping it, as Claude reads it: CLAUDE_CODE_USER_DIALOG_TIMEOUT_MS
// in its environment (envTimeout, empty when unknown), else dialogExpiry from
// managed and then the config root's user settings, never a project's, else
// the default. It is false when Claude keeps the message until someone
// answers.
func ClaudeHoldDeadline(root, envTimeout string) (time.Duration, bool) {
	if value := strings.TrimSpace(envTimeout); value != "" {
		if ms, err := strconv.ParseFloat(value, 64); err == nil {
			if ms <= 0 {
				return 0, false
			}
			return time.Duration(ms * float64(time.Millisecond)), true
		}
	}
	for _, path := range []string{claudeManagedSettingsPath(), ClaudeSettingsPath(root)} {
		data, _, err := ReadClaudeSettingsFile(path)
		if err != nil {
			continue
		}
		if deadline, ok := claudeDialogExpiries[gjson.GetBytes(data, "dialogExpiry").String()]; ok {
			return deadline, deadline > 0
		}
	}
	return ClaudeDefaultHoldDeadline, true
}

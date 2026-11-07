package surfaces

import (
	"encoding/json"
	"os"
	"strings"
	"time"
)

// claudeTurnStartBefore finds the prompt that opened a turn whose first rows
// the first read already saw, walking back from end. A finished turn before
// any prompt means the start is unknown.
func claudeTurnStartBefore(path string, end int64) (time.Time, bool) {
	file, err := os.Open(path)
	if err != nil {
		return time.Time{}, false
	}
	defer file.Close()
	var started time.Time
	found := false
	scanJSONLBackward(file, end, end, func(line []byte, _ int64) bool {
		var record claudeRecord
		if json.Unmarshal(line, &record) != nil {
			return true
		}
		if claudeRecordEndsTurn(record) || (record.Type == "assistant" && record.Message.StopReason == "end_turn") {
			return false
		}
		if turns := foldClaudeTurn(nil, record); len(turns) == 1 && turns[0].UserID != "" {
			started, found = turns[0].StartedAt, !turns[0].StartedAt.IsZero()
			return record.IsMeta && isClaudeStopHookFeedback(strings.TrimSpace(transcriptText(record.Message.Content)))
		}
		return true
	})
	return started, found
}

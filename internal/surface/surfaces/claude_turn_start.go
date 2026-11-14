package surfaces

import (
	"encoding/json"
	"os"
	"slices"
	"strings"
)

// claudeTurnBefore is the turn open at end, from the prompt that opened it and
// the messages queued into it, walking back from end; a finished turn before
// any prompt means none is.
func claudeTurnBefore(path string, end int64) (claudeTurn, bool) {
	file, err := os.Open(path)
	if err != nil {
		return claudeTurn{}, false
	}
	defer file.Close()
	var turn claudeTurn
	var queued []string
	found := false
	scanJSONLBackward(file, end, end, func(line []byte, _ int64) bool {
		var record claudeRecord
		if json.Unmarshal(line, &record) != nil {
			return true
		}
		if claudeRecordEndsTurn(record) || (record.Type == "assistant" && record.Message.StopReason == "end_turn") {
			return false
		}
		if id := claudeQueuedInput(record); id != "" {
			queued = append(queued, id)
			return true
		}
		if turns := foldClaudeTurn(nil, record); len(turns) == 1 && turns[0].UserID != "" {
			turn, found = turns[0], true
			return record.IsMeta && isClaudeStopHookFeedback(strings.TrimSpace(transcriptText(record.Message.Content)))
		}
		return true
	})
	if found {
		slices.Reverse(queued)
		turn.Inputs = append(turn.Inputs, queued...)
	}
	return turn, found
}

package surfaces

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/zm2231/agenthail/internal/surface"
)

const codexTranscriptTailBytes = 4 << 20

func codexTranscriptTail(ctx context.Context, path string, limit int) ([]surface.Exchange, error) {
	if path == "" {
		return nil, fmt.Errorf("no local transcript")
	}
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("no local transcript: %w", err)
	}
	lines, _, err := readRecentJSONLLines(ctx, path, 0, codexTranscriptTailBytes, maxCodexTranscriptRecordBytes)
	if err != nil {
		return nil, err
	}
	exchanges := make([]surface.Exchange, 0, limit)
	eventUsers := map[string]bool{}
	for _, line := range lines {
		var record map[string]any
		if json.Unmarshal(line, &record) != nil {
			continue
		}
		recordType := str(record, "type")
		for _, item := range codexTimelineItems(record) {
			switch item.Role {
			case "user":
				if item.Text != "" {
					key := str(record, "timestamp") + "\x00" + item.Text
					if recordType == "response_item" && eventUsers[key] {
						continue
					}
					if recordType == "event_msg" {
						eventUsers[key] = true
					}
					exchanges = append(exchanges, surface.Exchange{User: item.Text, Source: "local-transcript"})
				}
			case "assistant":
				if item.Text == "" {
					continue
				}
				if len(exchanges) == 0 || exchanges[len(exchanges)-1].Assistant != "" {
					exchanges = append(exchanges, surface.Exchange{Assistant: item.Text, Source: "local-transcript"})
				} else {
					exchanges[len(exchanges)-1].Assistant = item.Text
				}
			}
		}
	}
	if len(exchanges) > limit {
		exchanges = exchanges[len(exchanges)-limit:]
	}
	return exchanges, nil
}

package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func claudeTimelineItems(record map[string]any) []surface.TimelineItem {
	items := claudeRecordItems(record)
	id := claudeMessageID(record)
	if id == "" {
		return items
	}
	for i := range items {
		if items[i].Kind == "message" && (items[i].Role == "user" || items[i].Role == "peer") {
			items[i].MessageID = id
		}
	}
	return items
}

// Claude keeps the sender's id as msg_id on peer prompts and source_uuid on prompts absorbed mid-turn.
func claudeMessageID(record map[string]any) string {
	if attachment, _ := record["attachment"].(map[string]any); str(attachment, "type") == "queued_command" {
		return str(attachment, "source_uuid")
	}
	if str(record, "type") != "user" {
		return ""
	}
	origin, _ := record["origin"].(map[string]any)
	if id := str(origin, "msg_id"); id != "" {
		return id
	}
	return str(record, "uuid")
}

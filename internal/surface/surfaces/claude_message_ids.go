package surfaces

import "github.com/zm2231/agenthail/internal/surface"

func claudeTimelineItems(record map[string]any) []surface.TimelineItem {
	items := claudeRecordItems(record)
	id := claudeMessageID(record)
	promptID, turnOrigin := "", ""
	if str(record, "type") == "user" {
		promptID = str(record, "promptId")
		turnOrigin = str(record, "turnOrigin")
	}
	notPerson := turnOrigin != "" && turnOrigin != claudeOriginHuman
	for i := range items {
		if items[i].Kind == "message" && (items[i].Role == "user" || items[i].Role == "peer") {
			if id != "" {
				items[i].MessageID = id
			}
			items[i].PromptID = promptID
			if notPerson && items[i].Origin == "" {
				items[i].Origin = turnOrigin
			}
		} else if notPerson {
			items[i].PromptID = promptID
		}
	}
	return items
}

// Claude keeps a peer sender's id as origin.msg_id, both on the prompt that
// starts a turn and on one absorbed mid-turn; other prompts absorbed mid-turn
// carry theirs as source_uuid.
func claudeMessageID(record map[string]any) string {
	attachment, _ := record["attachment"].(map[string]any)
	queued := str(attachment, "type") == "queued_command"
	if !queued && str(record, "type") != "user" {
		return ""
	}
	origin, _ := record["origin"].(map[string]any)
	if queued {
		origin, _ = attachment["origin"].(map[string]any)
	}
	if id := str(origin, "msg_id"); id != "" {
		return id
	}
	if queued {
		return str(attachment, "source_uuid")
	}
	return str(record, "uuid")
}

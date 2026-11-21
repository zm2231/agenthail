package surfaces

import (
	"github.com/zm2231/agenthail/internal/surface"
	"github.com/zm2231/agenthail/internal/surface/transcript"
)

func claudeTimelineItems(record map[string]any) []surface.TimelineItem {
	items := claudeRecordItems(record)
	id := claudeMessageID(record)
	promptID, turnOrigin := "", ""
	if transcript.String(record, "type") == "user" {
		promptID = transcript.String(record, "promptId")
		turnOrigin = transcript.String(record, "turnOrigin")
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
	queued := transcript.String(attachment, "type") == "queued_command"
	if !queued && transcript.String(record, "type") != "user" {
		return ""
	}
	origin, _ := record["origin"].(map[string]any)
	if queued {
		origin, _ = attachment["origin"].(map[string]any)
	}
	if id := transcript.String(origin, "msg_id"); id != "" {
		return id
	}
	if queued {
		return transcript.String(attachment, "source_uuid")
	}
	return transcript.String(record, "uuid")
}
